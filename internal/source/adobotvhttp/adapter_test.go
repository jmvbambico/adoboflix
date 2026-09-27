package adobotvhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// The subscriber path is one of the two user choices: Selectable, never Dev.
func TestAdapterIsSelectableNotDev(t *testing.T) {
	if !source.Selectable(Name) {
		t.Errorf("Selectable(%q) = false, want true: it is an end-user path", Name)
	}
	if source.Dev(Name) {
		t.Errorf("Dev(%q) = true, want false", Name)
	}
	if !source.NeedsPlaylistCode(Name) {
		t.Errorf("NeedsPlaylistCode(%q) = false, want true", Name)
	}
}

func TestNameAndOptionalCapabilities(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, nil, standardDRM(), nil))

	if adapter.Name() != Name || Name != "adobotv-http" {
		t.Fatalf("Name = %q, want adobotv-http", adapter.Name())
	}

	var asSource source.Source = adapter
	if _, ok := asSource.(source.CompiledEPGProvider); !ok {
		t.Error("adapter does not implement source.CompiledEPGProvider")
	}
	if _, ok := asSource.(source.AccountInfoProvider); !ok {
		t.Error("adapter does not implement source.AccountInfoProvider: its envelope carries the account facts")
	}
	if _, ok := asSource.(source.StreamProbeLister); ok {
		t.Error("adapter must NOT implement source.StreamProbeLister: it only sees one playlist")
	}
}

func TestRegistryEntry(t *testing.T) {
	if err := source.Validate(Name); err != nil {
		t.Fatalf("source.Validate(%q) = %v, want nil (adapter init should register it)", Name, err)
	}
	found := false
	for _, name := range source.Available() {
		if name == Name {
			found = true
		}
	}
	if !found {
		t.Errorf("source.Available() = %v, want it to contain %q", source.Available(), Name)
	}
	if source.NeedsDatabase(Name) {
		t.Errorf("NeedsDatabase(%q) = true, want false: this adapter needs no database", Name)
	}
}

// Opaque ids are deterministic, so two refetches of the same envelope yield the
// same ids, and an id round-trips back through GetChannel.
func TestOpaqueIDStabilityAndRoundTrip(t *testing.T) {
	// CacheTTL 0 forces a fresh fetch on every call, so this test really does
	// compare two independent envelopes.
	adapter, _ := newTestServer(t, 0, libraryHandler(t, nil, standardDRM(), nil))

	first, total, err := adapter.ListChannels("", 100, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	second, _, err := adapter.ListChannels("", 100, 0)
	if err != nil {
		t.Fatalf("ListChannels (second fetch): %v", err)
	}

	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("id for %q changed between refetches: %q != %q", first[i].Name, first[i].ID, second[i].ID)
		}
		if !strings.HasPrefix(first[i].ID, "ch-") {
			t.Errorf("channel id %q lacks the ch- prefix", first[i].ID)
		}
		if strings.Count(first[i].ID, "-") != 1 {
			t.Errorf("channel id %q looks like it could be parsed as a uuid", first[i].ID)
		}
	}
	if first[0].ID == first[1].ID {
		t.Errorf("two channels share id %q", first[0].ID)
	}

	got, err := adapter.GetChannel(first[0].ID)
	if err != nil {
		t.Fatalf("GetChannel(%q): %v", first[0].ID, err)
	}
	if got.Name != first[0].Name {
		t.Errorf("GetChannel returned %q, want %q", got.Name, first[0].Name)
	}

	if _, err := adapter.GetChannel("ch-does-not-exist"); !errors.Is(err, ErrContentNotFound) {
		t.Errorf("GetChannel(unknown) = %v, want ErrContentNotFound", err)
	}
}

// The decoy channels[].url must never become playable, and no channel model
// exports a URL at all.
func TestDecoyURLNeverPlayable(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, nil, standardDRM(), nil))

	// Sanity: the fixture really does carry the decoy in channels[].url.
	env, err := adapter.fetchEnvelope(context.Background())
	if err != nil {
		t.Fatalf("fetchEnvelope: %v", err)
	}
	if env.Channels[0].URL != testDecoyURL {
		t.Fatalf("fixture channels[0].url = %q, want the decoy", env.Channels[0].URL)
	}

	channels, _, err := adapter.ListChannels("", 100, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	blob, err := json.Marshal(channels)
	if err != nil {
		t.Fatalf("marshal channels: %v", err)
	}
	for _, marker := range []string{"youtube.com", "dQw4w9WgXcQ", testDecoyURL} {
		if strings.Contains(string(blob), marker) {
			t.Errorf("channel JSON contains the decoy marker %q: %s", marker, blob)
		}
	}

	alpha := channelByName(t, channels, "Alpha TV")
	stream, err := adapter.ResolveChannelStream(alpha.ID)
	if err != nil {
		t.Fatalf("ResolveChannelStream: %v", err)
	}
	if stream.URL == testDecoyURL || strings.Contains(stream.URL, "youtube.com") {
		t.Fatalf("resolved stream plays the decoy: %q", stream.URL)
	}
	if !strings.HasPrefix(stream.URL, "https://cdn.example/live/alpha/") {
		t.Errorf("resolved stream url = %q, want the real runtime_attr_url target", stream.URL)
	}
	streamBlob, _ := json.Marshal(stream)
	if strings.Contains(string(streamBlob), "youtube.com") {
		t.Errorf("stream JSON contains the decoy: %s", streamBlob)
	}
}

func TestChannelMappingAndCategories(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, nil, standardDRM(), nil))

	channels, _, err := adapter.ListChannels("", 100, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	alpha := channelByName(t, channels, "Alpha TV")
	if alpha.Category == nil || *alpha.Category != "Movies" {
		t.Errorf("Alpha category = %v, want the display name Movies", alpha.Category)
	}
	if alpha.Logo == nil || *alpha.Logo != "https://img/a.png" {
		t.Errorf("Alpha logo = %v", alpha.Logo)
	}
	if alpha.EpgChannelID == nil || *alpha.EpgChannelID != "alpha.tvg" {
		t.Errorf("Alpha epg_channel_id = %v", alpha.EpgChannelID)
	}

	categories, err := adapter.ListChannelCategories()
	if err != nil {
		t.Fatalf("ListChannelCategories: %v", err)
	}
	if len(categories) != 2 || categories[0] != "Movies" || categories[1] != "News" {
		t.Errorf("categories = %v, want [Movies News]", categories)
	}

	for _, filter := range []string{"Movies", "movies", "MOVIES"} {
		filtered, total, err := adapter.ListChannels(filter, 100, 0)
		if err != nil {
			t.Fatalf("ListChannels(%q): %v", filter, err)
		}
		if total != 1 || len(filtered) != 1 || filtered[0].Name != "Alpha TV" {
			t.Errorf("ListChannels(%q) = %+v (total %d), want [Alpha TV]", filter, filtered, total)
		}
	}

	// Offset pagination mirrors postgres-direct's LIMIT/OFFSET contract.
	pageTwo, total, err := adapter.ListChannels("", 1, 1)
	if err != nil {
		t.Fatalf("ListChannels page 2: %v", err)
	}
	if total != 2 || len(pageTwo) != 1 {
		t.Errorf("page 2 = %d channels, total %d", len(pageTwo), total)
	}
}

func TestGetChannelWithStreams(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, nil, standardDRM(), nil))

	channels, _, err := adapter.ListChannels("", 100, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	beta := channelByName(t, channels, "Beta News")

	channel, streams, err := adapter.GetChannelWithStreams(beta.ID)
	if err != nil {
		t.Fatalf("GetChannelWithStreams: %v", err)
	}
	if channel.ID != beta.ID {
		t.Errorf("channel id = %q, want %q", channel.ID, beta.ID)
	}
	if len(streams) != 1 {
		t.Fatalf("streams = %d, want 1", len(streams))
	}
	if streams[0].URL != "https://cdn.example/live/beta/index.mpd" {
		t.Errorf("stream url = %q", streams[0].URL)
	}
	if streams[0].DrmType == nil || *streams[0].DrmType != "Widevine" || streams[0].LicenseURL == nil {
		t.Errorf("stream DRM = type %v license %v, want Widevine + license URL", streams[0].DrmType, streams[0].LicenseURL)
	}
	if streams[0].SourceType != sourceType || !streams[0].IsDefault {
		t.Errorf("stream meta = %+v", streams[0])
	}
}

func TestCompiledEPG(t *testing.T) {
	epg := []byte("\x1f\x8b\x08\x00gzipped-xmltv-fixture")
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, nil, standardDRM(), epg))

	data, hash, err := adapter.CompiledEPG()
	if err != nil {
		t.Fatalf("CompiledEPG: %v", err)
	}
	if string(data) != string(epg) {
		t.Errorf("EPG bytes = %q, want the served bytes", data)
	}
	sum := sha256.Sum256(epg)
	if hash != hex.EncodeToString(sum[:]) {
		t.Errorf("EPG hash = %q, want sha256 of the bytes", hash)
	}
}

// The envelope and VOD library are fetched once and then served from cache.
func TestCachingServesRepeatedReadsFromMemory(t *testing.T) {
	var playlistHits, vodHits int
	inner := libraryHandler(t, vodFixture, standardDRM(), nil)
	counting := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/playlist/"):
			playlistHits++
		case r.URL.Path == "/v1/vod":
			vodHits++
		}
		inner(w, r)
	}

	adapter, _ := newTestServer(t, 5*time.Minute, counting)
	if _, _, err := adapter.ListChannels("", 100, 0); err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if _, err := adapter.GetStats(); err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if _, _, err := adapter.ListChannels("", 100, 0); err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if _, _, err := adapter.GetEntries("", "", "", 1, 10); err != nil {
		t.Fatalf("GetEntries: %v", err)
	}

	if playlistHits != 1 {
		t.Errorf("playlist fetched %d times, want 1 (cached)", playlistHits)
	}
	if vodHits != 1 {
		t.Errorf("vod library fetched %d times, want 1 (cached)", vodHits)
	}
}

func TestConcurrentReadsAreSafe(t *testing.T) {
	adapter, _ := newTestServer(t, 5*time.Minute, libraryHandler(t, vodFixture, standardDRM(), nil))

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				_, _, err := adapter.ListChannels("", 100, 0)
				errs <- err
			case 1:
				_, _, err := adapter.GetEntries("", "", "", 1, 10)
				errs <- err
			case 2:
				_, err := adapter.GetStats()
				errs <- err
			case 3:
				_, err := adapter.ListChannelCategories()
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent read: %v", err)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"missing base url", map[string]string{}, EnvBaseURL},
		{"missing code", map[string]string{EnvBaseURL: "http://127.0.0.1:8080"}, EnvPlaylistCode},
		{"invalid base url", map[string]string{EnvBaseURL: "://nope", EnvPlaylistCode: "c"}, EnvBaseURL},
		{"non-http scheme", map[string]string{EnvBaseURL: "ftp://host", EnvPlaylistCode: "c"}, EnvBaseURL},
		{"bad ttl", map[string]string{EnvBaseURL: "http://host", EnvPlaylistCode: "c", EnvCacheTTL: "soon"}, EnvCacheTTL},
		{"negative ttl", map[string]string{EnvBaseURL: "http://host", EnvPlaylistCode: "c", EnvCacheTTL: "-1m"}, EnvCacheTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := configFrom(envFrom(tc.env))
			if err == nil {
				t.Fatalf("configFrom = nil error, want one naming %s", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not name %s", err, tc.wantErr)
			}
		})
	}

	cfg, err := configFrom(envFrom(map[string]string{
		EnvBaseURL:      "http://host:8080/",
		EnvPlaylistCode: "abc123456789",
	}))
	if err != nil {
		t.Fatalf("configFrom(valid): %v", err)
	}
	if cfg.BaseURL != "http://host:8080" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", cfg.BaseURL)
	}
	if cfg.UserAgent != DefaultUserAgent {
		t.Errorf("UserAgent = %q, want the default", cfg.UserAgent)
	}
	if cfg.CacheTTL != DefaultCacheTTL {
		t.Errorf("CacheTTL = %v, want %v", cfg.CacheTTL, DefaultCacheTTL)
	}

	overridden, err := configFrom(envFrom(map[string]string{
		EnvBaseURL:      "http://host",
		EnvPlaylistCode: "abc",
		EnvUserAgent:    "AdoboFlix/custom",
		EnvCacheTTL:     "90s",
	}))
	if err != nil {
		t.Fatalf("configFrom(overrides): %v", err)
	}
	if overridden.UserAgent != "AdoboFlix/custom" || overridden.CacheTTL != 90*time.Second {
		t.Errorf("overrides not applied: %+v", overridden)
	}
}

func TestNewFromEnvFailsFast(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvPlaylistCode, "")
	if _, err := NewFromEnv(); err == nil || !strings.Contains(err.Error(), EnvBaseURL) {
		t.Fatalf("NewFromEnv with no base URL = %v, want an error naming %s", err, EnvBaseURL)
	}

	t.Setenv(EnvBaseURL, "http://127.0.0.1:8080")
	if _, err := NewFromEnv(); err == nil || !strings.Contains(err.Error(), EnvPlaylistCode) {
		t.Fatalf("NewFromEnv with no playlist code = %v, want an error naming %s", err, EnvPlaylistCode)
	}

	t.Setenv(EnvPlaylistCode, "abc123456789")
	adapter, err := NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv(valid): %v", err)
	}
	if adapter == nil || adapter.Name() != Name {
		t.Fatalf("NewFromEnv returned %+v", adapter)
	}
}

func channelByName(t *testing.T, channels []source.Channel, name string) source.Channel {
	t.Helper()
	for _, c := range channels {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("channel %q not found in %+v", name, channels)
	return source.Channel{}
}

// Two channels that share a name and epg_id but differ by category must not
// collide: including the category is what stops the second being shadowed.
func TestChannelIDIncludesCategory(t *testing.T) {
	a := channelID("Same Name", "same.tvg", "movies")
	b := channelID("Same Name", "same.tvg", "news")
	if a == b {
		t.Fatalf("channelID ignored the category: both ids are %q", a)
	}
}

// Concurrent readers that all miss the cache must collapse onto ONE upstream
// fetch: fetchMu is held across the fetch and the cache is re-checked inside the
// lock, so the waiters return the first fetch's result instead of stampeding.
func TestConcurrentCacheMissesCollapseToOneFetch(t *testing.T) {
	var playlistHits atomic.Int32
	inner := libraryHandler(t, nil, standardDRM(), nil)
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/playlist/") {
			playlistHits.Add(1)
			// Widen the window so every goroutine has time to miss the cold
			// cache and pile up on fetchMu.
			time.Sleep(20 * time.Millisecond)
		}
		inner(w, r)
	}

	adapter, _ := newTestServer(t, time.Minute, handler)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, _, err := adapter.ListChannels("", 100, 0); err != nil {
				t.Errorf("ListChannels: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := playlistHits.Load(); got != 1 {
		t.Errorf("playlist fetched %d times, want exactly 1 (concurrent misses must collapse)", got)
	}
}

// A rejected content token must drop the cached envelope so the next read
// refetches and mints a fresh token, instead of re-sending the dead one until
// the TTL lapses. It also surfaces as its own sentinel, not the generic
// upstream error a dead token would otherwise look like.
func TestTokenRejectionInvalidatesCacheAndRefetches(t *testing.T) {
	var playlistHits atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/playlist/"+testPlaylistCode {
			playlistHits.Add(1)
			writeBody(w, http.StatusOK, "application/json", string(envelopeBody(t, r, nil)))
			return
		}
		// A token message that also says "expired" — the token branch must win
		// over the subscription branch.
		writeErrorEnvelope(w, http.StatusForbidden, "Invalid or expired content token")
	}

	adapter, srv := newTestServer(t, time.Minute, handler)

	if _, _, err := adapter.ListChannels("", 100, 0); err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if got := playlistHits.Load(); got != 1 {
		t.Fatalf("playlist fetched %d times before the rejection, want 1", got)
	}

	_, err := adapter.resolveRuntimeAttr(context.Background(), srv.URL+"/v1/drm/key/ch-alpha?token="+testToken)
	if !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("error = %v, want it to wrap ErrTokenRejected", err)
	}
	if errors.Is(err, ErrSubscriptionInactive) {
		t.Errorf("token rejection misfiled as a subscription problem: %v", err)
	}

	if _, _, err := adapter.ListChannels("", 100, 0); err != nil {
		t.Fatalf("ListChannels after rejection: %v", err)
	}
	if got := playlistHits.Load(); got != 2 {
		t.Errorf("playlist fetched %d times, want 2 (cache invalidated on token rejection)", got)
	}
}
