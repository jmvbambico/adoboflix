package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/epg"
	"github.com/jmvbambico/adoboflix/internal/playlistcode"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
)

// codeStubSource is a source.Source stub for the playlist-code endpoints. It
// reports the error its ListChannels returns, which is how a candidate adapter
// is validated, and is otherwise inert.
type codeStubSource struct {
	source.Source
	name    string
	listErr error
}

func (s *codeStubSource) Name() string { return s.name }
func (s *codeStubSource) GetStats() (*source.Stats, error) {
	return &source.Stats{}, nil
}
func (s *codeStubSource) ListChannels(string, int, int) ([]source.Channel, int, error) {
	return nil, 0, s.listErr
}

func newTestSourceHandler(t *testing.T, cfg source.Config, envCode string) (*SourceHandler, *PlayerHandler, *playlistcode.Store) {
	t.Helper()
	store := playlistcode.New(filepath.Join(t.TempDir(), "playlist-code"))
	player := NewPlayerHandler(&codeStubSource{name: "initial"})
	return NewSourceHandler(player, store, cfg, envCode), player, store
}

func sourceControlRouter(h *SourceHandler) *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/source/status", h.GetStatus)
	r.POST("/api/v1/source/playlist-code", h.SetPlaylistCode)
	r.DELETE("/api/v1/source/playlist-code", h.DeletePlaylistCode)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Status must report the source and its configured state, and must never carry
// the code — not the stored one, and not the environment fallback either.
func TestSourceStatusNeverContainsCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const stored = "STORED-SECRET-CODE-AAA"
	const envCode = "ENV-SECRET-CODE-BBB"

	h, _, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, envCode)
	if err := store.Save(stored); err != nil {
		t.Fatalf("Save: %v", err)
	}

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

	var got struct {
		Source     string `json:"source"`
		Needs      bool   `json:"needs_playlist_code"`
		Configured bool   `json:"playlist_code_configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got.Source != adobotvhttp.Name {
		t.Errorf("source = %q, want %q", got.Source, adobotvhttp.Name)
	}
	if !got.Needs {
		t.Error("needs_playlist_code = false, want true for adobotv-http")
	}
	if !got.Configured {
		t.Error("playlist_code_configured = false, want true with a stored code")
	}
}

// With neither a stored nor an environment code the source is unconfigured —
// the state a new subscriber starts in — and status says so without erroring.
func TestSourceStatusUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")

	w := doJSON(t, sourceControlRouter(h), http.MethodGet, "/api/v1/source/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Needs      bool `json:"needs_playlist_code"`
		Configured bool `json:"playlist_code_configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !got.Needs || got.Configured {
		t.Errorf("status = %+v, want needs=true configured=false", got)
	}
}

// A source that takes no playlist code reports needs=false and is never
// offered the entry path.
func TestSourceStatusForNonCodeSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: "file"}, "")

	w := doJSON(t, sourceControlRouter(h), http.MethodGet, "/api/v1/source/status", "")
	var got struct {
		Needs      bool `json:"needs_playlist_code"`
		Configured bool `json:"playlist_code_configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got.Needs || got.Configured {
		t.Errorf("status = %+v, want needs=false configured=false", got)
	}
}

// A rejected code persists nothing and swaps nothing, and its gate code reaches
// the client unchanged.
func TestSetPlaylistCodeRejectedPersistsNothingAndSwapsNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	before := player.src()

	var openedWith string
	h.open = func(cfg source.Config) (source.Source, error) {
		openedWith = cfg.PlaylistCode
		return &codeStubSource{name: "candidate", listErr: fmt.Errorf("%w: HTTP 403", adobotvhttp.ErrPlaylistRejected)}, nil
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-code", `{"code":"BAD-CODE"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	var got struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != codePlaylistRejected {
		t.Errorf("code = %q, want %q", got.Code, codePlaylistRejected)
	}
	if openedWith != "BAD-CODE" {
		t.Errorf("candidate opened with %q, want the submitted code", openedWith)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Errorf("store after a rejected code = (ok=%v, err=%v), want nothing persisted", ok, err)
	}
	if player.src() != before {
		t.Error("a rejected code swapped the live source; it must not")
	}
}

// Every validation failure maps to the one gate code the rest of the API uses,
// and none of them persists or swaps.
func TestSetPlaylistCodeValidationFailuresMapToGateCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name     string
		err      error
		wantHTTP int
		wantCode string
	}{
		{"rejected", fmt.Errorf("%w: 403", adobotvhttp.ErrPlaylistRejected), http.StatusForbidden, codePlaylistRejected},
		{"device pending", fmt.Errorf("%w: splash", adobotvhttp.ErrDevicePending), http.StatusForbidden, codeDevicePending},
		{"subscription inactive", fmt.Errorf("%w: not active", adobotvhttp.ErrSubscriptionInactive), http.StatusForbidden, codeSubscriptionInactive},
		{"user agent rejected", fmt.Errorf("%w: prefix", adobotvhttp.ErrUserAgentRejected), http.StatusForbidden, codeUserAgentRejected},
		{"no code configured", fmt.Errorf("%w: none", adobotvhttp.ErrNoPlaylistCode), http.StatusForbidden, codePlaylistCodeRequired},
		{"upstream", fmt.Errorf("%w: refused", adobotvhttp.ErrUpstream), http.StatusBadGateway, codeUpstreamError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
			before := player.src()
			h.open = func(source.Config) (source.Source, error) {
				return &codeStubSource{name: "candidate", listErr: tc.err}, nil
			}

			w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-code", `{"code":"SOME-CODE"}`)
			if w.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantHTTP)
			}
			var got struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", got.Code, tc.wantCode)
			}
			if _, ok, err := store.Load(); err != nil || ok {
				t.Errorf("store = (ok=%v, err=%v), want nothing persisted", ok, err)
			}
			if player.src() != before {
				t.Error("a validation failure swapped the live source; it must not")
			}
		})
	}
}

// A valid code is validated with a real call, then persisted and swapped, and
// the response never echoes it.
func TestSetPlaylistCodeValidPersistsAndSwaps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const code = "GOOD-CODE-123"
	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")

	candidate := &codeStubSource{name: "candidate"} // ListChannels succeeds
	opened := false
	h.open = func(cfg source.Config) (source.Source, error) {
		opened = true
		if cfg.PlaylistCode != code {
			t.Errorf("candidate opened with %q, want %q", cfg.PlaylistCode, code)
		}
		return candidate, nil
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-code", `{"code":"`+code+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !opened {
		t.Error("the candidate adapter was never opened for validation")
	}
	if strings.Contains(w.Body.String(), code) {
		t.Errorf("response leaked the code: %s", w.Body.String())
	}
	stored, ok, err := store.Load()
	if err != nil || !ok || stored != code {
		t.Fatalf("store = (%q, %v, %v), want the code persisted", stored, ok, err)
	}
	if player.src() != source.Source(candidate) {
		t.Error("the live source was not swapped to the validated candidate")
	}
}

// A malformed or empty body is a 400 and touches nothing.
func TestSetPlaylistCodeRejectsEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"code":""}`, `{"code":"   "}`, `{}`, `not json`} {
		h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
		before := player.src()
		h.open = func(source.Config) (source.Source, error) {
			t.Error("a malformed body must not open a candidate")
			return nil, nil
		}

		w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-code", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, w.Code)
		}
		if _, ok, _ := store.Load(); ok {
			t.Errorf("body %q: something was persisted", body)
		}
		if player.src() != before {
			t.Errorf("body %q: the source was swapped", body)
		}
	}
}

// The entry path is not offered for a source that takes no code.
func TestSetPlaylistCodeNotSupportedForNonCodeSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, store := newTestSourceHandler(t, source.Config{Name: "file"}, "")
	h.open = func(source.Config) (source.Source, error) {
		t.Error("the entry path must not open a candidate for a non-code source")
		return nil, nil
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-code", `{"code":"X"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	var got struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != codePlaylistCodeNotSupported {
		t.Errorf("code = %q, want %q", got.Code, codePlaylistCodeNotSupported)
	}
	if _, ok, _ := store.Load(); ok {
		t.Error("something was persisted for a non-code source")
	}
}

// DELETE clears the stored code and reopens from the environment fallback,
// which then wins again.
func TestDeletePlaylistCodeClearsAndFallsBackToEnv(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const envCode = "ENV-FALLBACK-CODE"
	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, envCode)
	if err := store.Save("STORED-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	before := player.src()

	reopened := &codeStubSource{name: "reopened"}
	var openedWith string
	h.open = func(cfg source.Config) (source.Source, error) {
		openedWith = cfg.PlaylistCode
		return reopened, nil
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodDelete, "/api/v1/source/playlist-code", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Configured bool `json:"playlist_code_configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("store after DELETE = (ok=%v, err=%v), want cleared", ok, err)
	}
	if openedWith != envCode {
		t.Errorf("reopened with %q, want the env fallback", openedWith)
	}
	if !got.Configured {
		t.Error("playlist_code_configured = false, want true from the env fallback")
	}
	if player.src() == before {
		t.Error("DELETE did not reopen the source")
	}
	if player.src() != source.Source(reopened) {
		t.Error("DELETE did not reopen the source from the env fallback")
	}
}

// DELETE with no environment fallback leaves the source unconfigured.
func TestDeletePlaylistCodeLeavesSourceUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	if err := store.Save("STORED-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var openedWith = "unset"
	h.open = func(cfg source.Config) (source.Source, error) {
		openedWith = cfg.PlaylistCode
		return &codeStubSource{name: "unconfigured"}, nil
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodDelete, "/api/v1/source/playlist-code", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if openedWith != "" {
		t.Errorf("reopened with %q, want the empty (unconfigured) code", openedWith)
	}
	var got struct {
		Needs      bool `json:"needs_playlist_code"`
		Configured bool `json:"playlist_code_configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !got.Needs || got.Configured {
		t.Errorf("status = %+v, want needs=true configured=false", got)
	}
}

// swapStubSource is a source that also supplies EPG bytes (as an error, so the
// service stays empty), exercising both atomics a swap touches.
type swapStubSource struct {
	source.Source
	tag string
}

func (s *swapStubSource) Name() string { return s.tag }
func (s *swapStubSource) GetStats() (*source.Stats, error) {
	return &source.Stats{}, nil
}
func (s *swapStubSource) ListChannels(string, int, int) ([]source.Channel, int, error) {
	return nil, 0, nil
}
func (s *swapStubSource) GetChannel(id string) (*source.Channel, error) {
	epgID := "epg-" + id
	return &source.Channel{ID: id, EpgChannelID: &epgID}, nil
}
func (s *swapStubSource) CompiledEPG() ([]byte, string, error) {
	return nil, "", errors.New("no EPG data in this test")
}

// This is the test that matters: reads hot and swaps frequent, run under -race.
// A swap must never race an in-flight read of either the source or the EPG
// service.
func TestSwapSourceIsRaceFreeUnderConcurrentReads(t *testing.T) {
	gin.SetMode(gin.TestMode)

	a := &swapStubSource{tag: "a"}
	b := &swapStubSource{tag: "b"}
	player := NewPlayerHandler(a)
	// Prime the EPG service exactly as the server does, so the swap exercises
	// the EPG pointer with a live service rather than the unavailable path.
	player.WithEPG(epg.NewService(a))

	r := gin.New()
	r.GET("/stats", player.GetStats)
	r.GET("/channels", player.ListChannels)
	r.GET("/epg/:id", player.GetChannelEPG)

	const (
		readers    = 8
		iterations = 800
		swaps      = 250
	)
	paths := []string{"/stats", "/channels", "/epg/c1"}

	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < iterations; j++ {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, paths[(i+j)%len(paths)], nil))
				if w.Code >= http.StatusInternalServerError {
					t.Errorf("%s: status %d", paths[(i+j)%len(paths)], w.Code)
					return
				}
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < swaps; i++ {
			if i%2 == 0 {
				player.SwapSource(a)
			} else {
				player.SwapSource(b)
			}
		}
	}()

	close(start)
	wg.Wait()

	final := player.src()
	if final != source.Source(a) && final != source.Source(b) {
		t.Errorf("active source after the swaps is %v, want one of the swapped sources", final)
	}
}
