package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
)

// accountCapableSource is a codeStubSource that can also describe its account,
// mirroring the adapter's optional source.AccountInfoProvider capability.
type accountCapableSource struct {
	*codeStubSource
	info source.AccountInfo
	err  error
}

func (s *accountCapableSource) AccountInfo(context.Context) (source.AccountInfo, error) {
	return s.info, s.err
}

// accountOnlySource forwards a Source while deliberately hiding every other
// optional capability. Swapping it in therefore starts no EPG service — its own
// upstream fetch would confound the "status made no request" assertion below —
// while still exposing the wrapped adapter's AccountInfo through the capability
// the status handler type-asserts for.
type accountOnlySource struct {
	source.Source
}

func (s accountOnlySource) AccountInfo(ctx context.Context) (source.AccountInfo, error) {
	return s.Source.(source.AccountInfoProvider).AccountInfo(ctx)
}

// statusBody issues GET /source/status and decodes the JSON object, so each
// test asserts on presence and absence of keys rather than a struct that would
// hide an unexpected field.
func statusBody(t *testing.T, h *SourceHandler) map[string]any {
	t.Helper()
	w := doJSON(t, sourceControlRouter(h), http.MethodGet, "/api/v1/source/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return body
}

// A source that can supply account facts, with a code configured, surfaces the
// expiry and the upstream message without disturbing the existing fields.
func TestSourceStatusIncludesAccountFacts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const code = "STORED-CODE"
	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	if err := store.Save(code); err != nil {
		t.Fatalf("Save: %v", err)
	}
	expiry := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	player.SwapSource(&accountCapableSource{
		codeStubSource: &codeStubSource{name: adobotvhttp.Name},
		info: source.AccountInfo{
			SubscriptionExpiresAt: &expiry,
			UserMessage:           "Renew by Friday.",
		},
	})

	body := statusBody(t, h)
	if got := body["subscription_expires_at"]; got != "2030-01-01T00:00:00Z" {
		t.Errorf("subscription_expires_at = %v, want 2030-01-01T00:00:00Z", got)
	}
	if got := body["user_message"]; got != "Renew by Friday." {
		t.Errorf("user_message = %v, want the upstream message", got)
	}
	// The existing contract is unchanged.
	if body["source"] != adobotvhttp.Name {
		t.Errorf("source = %v, want %q", body["source"], adobotvhttp.Name)
	}
	if body["needs_playlist_code"] != true || body["playlist_code_configured"] != true {
		t.Errorf("status = %v, want needs=true configured=true", body)
	}
}

// Upstream supplied neither fact: both keys are omitted, not rendered empty.
func TestSourceStatusOmitsAbsentAccountFacts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	if err := store.Save("STORED-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	player.SwapSource(&accountCapableSource{codeStubSource: &codeStubSource{name: adobotvhttp.Name}})

	body := statusBody(t, h)
	for _, key := range []string{"subscription_expires_at", "user_message"} {
		if _, ok := body[key]; ok {
			t.Errorf("status carries %q = %v, want it omitted when upstream said nothing", key, body[key])
		}
	}
	if body["playlist_code_configured"] != true {
		t.Errorf("status = %v, want the base fields intact", body)
	}
}

// A source that takes a code but has no account concept (file, postgres-direct)
// does not implement the capability, so the keys are omitted even with a
// configured state — the type assertion, not the config name, decides.
func TestSourceStatusOmitsAccountFactsForSourceWithoutCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The name is adobotv-http so a code is configured and the capability branch
	// really runs; the player's source is the plain codeStubSource, which has no
	// AccountInfo.
	h, _, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	if err := store.Save("STORED-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := statusBody(t, h)
	if body["playlist_code_configured"] != true {
		t.Fatalf("status = %v, want configured=true so the capability branch is exercised", body)
	}
	for _, key := range []string{"subscription_expires_at", "user_message"} {
		if _, ok := body[key]; ok {
			t.Errorf("status carries %q for a source without the capability", key)
		}
	}
}

// An upstream refusal while reading the account facts must not turn the
// always-answers status endpoint into an error.
func TestSourceStatusOmitsAccountFactsWhenProviderErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	if err := store.Save("STORED-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	player.SwapSource(&accountCapableSource{
		codeStubSource: &codeStubSource{name: adobotvhttp.Name},
		err:            errors.New("upstream refused"),
	})

	body := statusBody(t, h) // fails the test if the status is not 200
	for _, key := range []string{"subscription_expires_at", "user_message"} {
		if _, ok := body[key]; ok {
			t.Errorf("status carries %q on a provider error", key)
		}
	}
}

// The account fields are an addition, not a hole: even with real account facts
// present, the playlist code never appears in the response.
func TestSourceStatusWithAccountFactsNeverContainsCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const stored = "STORED-SECRET-CODE-AAA"
	const envCode = "ENV-SECRET-CODE-BBB"
	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, envCode)
	if err := store.Save(stored); err != nil {
		t.Fatalf("Save: %v", err)
	}
	expiry := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	player.SwapSource(&accountCapableSource{
		codeStubSource: &codeStubSource{name: adobotvhttp.Name},
		info: source.AccountInfo{
			SubscriptionExpiresAt: &expiry,
			UserMessage:           "Renew by Friday.",
		},
	})

	w := doJSON(t, sourceControlRouter(h), http.MethodGet, "/api/v1/source/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	raw := w.Body.String()
	for _, secret := range []string{stored, envCode} {
		if strings.Contains(raw, secret) {
			t.Errorf("status body leaked the code %q: %s", secret, raw)
		}
	}
}

// TestSourceStatusDoesNotStallOnAColdUpstream is the property this round exists
// for: with a code configured and an upstream that accepts the connection but
// never answers, status still returns promptly and simply omits the account
// facts. It fails against an AccountInfo that fetches, because the handler would
// block on the read for the client's whole 30s timeout.
func TestSourceStatusDoesNotStallOnAColdUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// An upstream that never responds while the test runs.
	release := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
		<-release
	}))
	// Registered first so it is closed last (cleanups run LIFO), after the
	// release below has unblocked any in-flight handler.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	t.Setenv(adobotvhttp.EnvBaseURL, srv.URL)
	coldSource, err := source.Open(source.Config{Name: adobotvhttp.Name, PlaylistCode: "SOME-CODE"})
	if err != nil {
		t.Fatalf("source.Open: %v", err)
	}

	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	if err := store.Save("SOME-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Wrap it so the swap does not also start the EPG service; the property
	// under test is the account read, and an EPG refresh would fetch upstream
	// on its own.
	player.SwapSource(accountOnlySource{Source: coldSource})

	type result struct {
		status int
		body   map[string]any
	}
	done := make(chan result, 1)
	go func() {
		w := doJSON(t, sourceControlRouter(h), http.MethodGet, "/api/v1/source/status", "")
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			done <- result{status: -1}
			return
		}
		done <- result{status: w.Code, body: body}
	}()

	select {
	case got := <-done:
		if got.status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %v", got.status, got.body)
		}
		for _, key := range []string{"subscription_expires_at", "user_message"} {
			if _, ok := got.body[key]; ok {
				t.Errorf("status carries %q for a cold upstream: %v", key, got.body)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("status did not return within 2s; the account read stalled on a never-answering upstream")
	}

	if got := hits.Load(); got != 0 {
		t.Errorf("status made %d upstream request(s); the account read must not fetch", got)
	}
}
