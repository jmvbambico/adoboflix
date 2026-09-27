package adobotvhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// newSyncTestAdapter builds an adapter with an injectable clock and its own
// httptest server, so Refresh can be exercised against controlled upstream
// conditions without real time.
func newSyncTestAdapter(t *testing.T, ttl time.Duration, clock func() time.Time, handler http.HandlerFunc) (*Adapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	adapter, err := newWithConfig(Config{
		BaseURL:      srv.URL,
		PlaylistCode: testPlaylistCode,
		UserAgent:    "AdoboFlix-test",
		CacheTTL:     ttl,
		HTTPClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: srv.Client().Transport,
		},
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("newWithConfig: %v", err)
	}
	return adapter, srv
}

// Refresh fetches the library and records when, so status can report it without
// touching the network. Before any fetch there is no time to report.
func TestRefreshRecordsLastSyncedAt(t *testing.T) {
	at := time.Date(2026, 3, 12, 9, 30, 0, 0, time.UTC)
	adapter, _ := newSyncTestAdapter(t, time.Hour, func() time.Time { return at },
		libraryHandler(t, nil, standardDRM(), nil))

	if _, ok := adapter.LastSyncedAt(); ok {
		t.Fatal("LastSyncedAt before any fetch = ok, want false")
	}

	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, ok := adapter.LastSyncedAt()
	if !ok {
		t.Fatal("LastSyncedAt after Refresh = not ok, want ok")
	}
	if !got.Equal(at) {
		t.Errorf("LastSyncedAt = %v, want the injected clock time %v", got, at)
	}
	// The refresh really did warm the library.
	if _, err := adapter.GetStats(); err != nil {
		t.Fatalf("GetStats after Refresh: %v", err)
	}
}

// Refresh bypasses the cache TTL: the point of a manual sync is to pull now,
// not to wait out the five-minute window.
func TestRefreshRefetchesWithinTTL(t *testing.T) {
	var hits atomic.Int32
	inner := libraryHandler(t, nil, standardDRM(), nil)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/playlist/") {
			hits.Add(1)
		}
		inner(w, r)
	}
	adapter, _ := newSyncTestAdapter(t, time.Hour, time.Now, handler)

	if _, _, err := adapter.ListChannels("", 100, 0); err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("playlist fetched %d times before Refresh, want 1", got)
	}

	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// A normal read within the TTL is served from cache — proving the extra
	// fetch below came from Refresh and not from the read.
	if _, _, err := adapter.ListChannels("", 100, 0); err != nil {
		t.Fatalf("ListChannels after Refresh: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("playlist fetched %d times, want 2 (Refresh refetches despite the TTL)", got)
	}
}

// A failed refresh must not disturb the cache the client is already serving
// from: the next read stays warm and does not pay a refetch. This is the
// "leave the cache alone on failure" property.
func TestRefreshFailureLeavesCacheIntact(t *testing.T) {
	var fail atomic.Bool
	var hits atomic.Int32
	inner := libraryHandler(t, nil, standardDRM(), nil)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/playlist/") {
			hits.Add(1)
			if fail.Load() {
				writeErrorEnvelope(w, http.StatusInternalServerError, "upstream is down")
				return
			}
		}
		inner(w, r)
	}

	base := time.Date(2026, 3, 12, 9, 30, 0, 0, time.UTC)
	clock := func() time.Time { return base }
	adapter, _ := newSyncTestAdapter(t, time.Hour, clock, handler)

	// Warm the cache with a successful read.
	if _, _, err := adapter.ListChannels("", 100, 0); err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if _, ok := adapter.LastSyncedAt(); !ok {
		t.Fatal("LastSyncedAt after a read = not ok, want ok")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("playlist fetched %d times, want 1", got)
	}

	fail.Store(true)
	if err := adapter.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh against a failing upstream = nil, want an error")
	}
	// The failed Refresh did attempt its own fetch; capture the count so the
	// assertion below measures the read that follows, not the attempt.
	afterFailedRefresh := hits.Load()
	if afterFailedRefresh != 2 {
		t.Fatalf("playlist fetched %d times, want 2 (one warm read + the failed Refresh)", afterFailedRefresh)
	}

	// The cached library is still served, and — because it is still cached —
	// the read does not refetch. A failed refresh must not evict the cache.
	channels, total, err := adapter.ListChannels("", 100, 0)
	if err != nil {
		t.Fatalf("ListChannels after a failed Refresh: %v", err)
	}
	if total != 2 || len(channels) != 2 {
		t.Errorf("cached channels = %d (total %d), want the 2 from before the failure", len(channels), total)
	}
	if got := hits.Load(); got != afterFailedRefresh {
		t.Errorf("playlist fetched %d times after a failed Refresh, want still %d: the read was not served from the intact cache", got, afterFailedRefresh)
	}
	if at, ok := adapter.LastSyncedAt(); !ok || !at.Equal(base) {
		t.Errorf("LastSyncedAt = (%v, %v), want it unchanged at %v", at, ok, base)
	}
}

// The optional capabilities are present, and the sync they imply is honest: the
// returned Source interface still exposes no write path.
func TestAdapterIsSyncProvider(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, nil, standardDRM(), nil))
	var asSource source.Source = adapter
	if _, ok := asSource.(source.SyncProvider); !ok {
		t.Error("adapter does not implement source.SyncProvider: it has an upstream library to refresh")
	}
	if _, ok := asSource.(source.ContextChannelLister); !ok {
		t.Error("adapter does not implement source.ContextChannelLister: its fetch must be cancellable")
	}
}

// ListChannelsContext carries the caller's context into the upstream request, so
// cancelling it aborts an in-flight fetch. This is what stops a shutdown from
// waiting out the adapter's HTTP timeout. The server blocks until the client
// cancels, so the only ways this can pass are the context reaching the request
// (the error returns) or the deadline firing (the test fails) — a
// context-free fetch would hang here.
func TestListChannelsContextCancelsInFlightFetch(t *testing.T) {
	arrived := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/playlist/") {
			close(arrived)
			<-r.Context().Done()
			return
		}
		writeErrorEnvelope(w, http.StatusNotFound, "unexpected path")
	}
	adapter, _ := newSyncTestAdapter(t, time.Hour, time.Now, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := adapter.ListChannelsContext(ctx, "", 1, 0)
		errCh <- err
	}()

	<-arrived
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("a cancelled fetch returned nil, want an error")
		}
		if !errors.Is(err, ErrUpstream) {
			t.Errorf("error = %v, want it to wrap ErrUpstream", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled fetch did not return: the context did not reach the request")
	}
}
