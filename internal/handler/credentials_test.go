package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/source"
)

// TestStashRemovesUserInfoAndRemembersIt is the core of the fix: what resolve
// hands the browser must not contain credentials, and the proxy must still be
// able to find them.
func TestStashRemovesUserInfoAndRemembersIt(t *testing.T) {
	s := newCredentialStore()

	got := s.stash("https://sub:secret@cdn.example.com/live/stream.mpd?x=1")
	if want := "https://cdn.example.com/live/stream.mpd?x=1"; got != want {
		t.Errorf("stash() = %q, want %q", got, want)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "sub") {
		t.Errorf("stash() leaked credentials: %q", got)
	}

	creds, ok := s.lookup("https://cdn.example.com/live/stream.mpd?x=1")
	if !ok {
		t.Fatal("lookup() found nothing for the stashed origin")
	}
	if creds.user != "sub" || creds.pass != "secret" {
		t.Errorf("lookup() = %q/%q, want sub/secret", creds.user, creds.pass)
	}
}

// TestLookupCoversSegmentsOnTheSameOrigin pins the reason the key is the origin
// and not the full URL: a player derives segment URLs from the manifest, so the
// proxy is asked for URLs resolve never saw.
func TestLookupCoversSegmentsOnTheSameOrigin(t *testing.T) {
	s := newCredentialStore()
	s.stash("https://sub:secret@cdn.example.com/live/stream.mpd")

	if _, ok := s.lookup("https://cdn.example.com/live/seg-00042.m4s"); !ok {
		t.Error("segment on the manifest's origin got no credentials")
	}
	if _, ok := s.lookup("https://other.example.com/live/seg-00042.m4s"); ok {
		t.Error("a different origin was given another origin's credentials")
	}
}

func TestStashLeavesPlainURLsAlone(t *testing.T) {
	s := newCredentialStore()

	for _, raw := range []string{
		"https://cdn.example.com/live/stream.mpd",
		"http://127.0.0.1:8080/a/b.m3u8?token=abc",
		"://not a url",
	} {
		if got := s.stash(raw); got != raw {
			t.Errorf("stash(%q) = %q, want it unchanged", raw, got)
		}
	}
	if len(s.byOrigin) != 0 {
		t.Errorf("stash recorded %d entries for URLs with no userinfo", len(s.byOrigin))
	}
}

// TestLookupPrefersTheCallersOwnUserInfo: the proxy is reachable directly with
// a URL resolve never produced, and that caller's credentials are the more
// specific ones.
func TestLookupPrefersTheCallersOwnUserInfo(t *testing.T) {
	s := newCredentialStore()
	s.stash("https://stashed:old@cdn.example.com/a.mpd")

	creds, ok := s.lookup("https://direct:new@cdn.example.com/b.mpd")
	if !ok {
		t.Fatal("lookup() found nothing")
	}
	if creds.user != "direct" || creds.pass != "new" {
		t.Errorf("lookup() = %q/%q, want direct/new", creds.user, creds.pass)
	}
}

func TestStripUserInfo(t *testing.T) {
	cases := map[string]string{
		"https://u:p@cdn.example.com/a.mpd": "https://cdn.example.com/a.mpd",
		"https://cdn.example.com/a.mpd":     "https://cdn.example.com/a.mpd",
		"://not a url":                      "://not a url",
	}
	for in, want := range cases {
		if got := stripUserInfo(in); got != want {
			t.Errorf("stripUserInfo(%q) = %q, want %q", in, got, want)
		}
	}
}

// credStubSource serves one channel stream whose URL carries userinfo, the
// shape internal/source/adobotvhttp's injectUserInfo produces.
type credStubSource struct {
	source.Source
	streamURL string
}

func (s *credStubSource) Name() string { return "cred-stub" }

func (s *credStubSource) ResolveChannelStream(channelID string) (*source.Stream, error) {
	return &source.Stream{ID: "s1", ChannelID: channelID, URL: s.streamURL, SourceType: "adobotv"}, nil
}

// TestResolveDoesNotLeakCredentialsToTheBrowser is the end-to-end assertion:
// the JSON the page receives carries the CDN URL but no user:pass.
func TestResolveDoesNotLeakCredentialsToTheBrowser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewPlayerHandler(&credStubSource{streamURL: "https://sub:secret@cdn.example.com/live/stream.mpd"})
	r := gin.New()
	r.GET("/channels/:id/resolve", h.ResolveChannelStream)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/channels/c1/resolve", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if strings.Contains(body.URL, "secret") || strings.Contains(body.URL, "sub:") {
		t.Errorf("resolve leaked credentials to the browser: %q", body.URL)
	}

	// The wrapped URL must still be the usable CDN URL, credentials aside.
	inner := innerProxyTarget(t, body.URL)
	if inner != "https://cdn.example.com/live/stream.mpd" {
		t.Errorf("proxied target = %q, want the credential-free CDN URL", inner)
	}
}

// TestProxyAttachesCredentialsAsAHeader proves the credentials are not lost:
// the proxy re-attaches them upstream, out of the URL, where the browser never
// sees them.
func TestProxyAttachesCredentialsAsAHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotUser, gotPass string
	var gotOK bool
	var gotRequestURI string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotUser, gotPass, gotOK = req.BasicAuth()
		gotRequestURI = req.RequestURI
		w.Write([]byte("ok"))
	}))
	defer origin.Close()

	streamURL := strings.Replace(origin.URL, "http://", "http://sub:secret@", 1) + "/live/stream.mpd"
	h := NewPlayerHandler(&credStubSource{streamURL: streamURL})
	r := gin.New()
	r.GET("/channels/:id/resolve", h.ResolveChannelStream)
	r.GET("/api/v1/proxy", h.ProxyStream)

	// Resolve first: that is what stashes the credentials.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/channels/c1/resolve", nil))
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	// Then play it, the way the player does.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/proxy?url="+url.QueryEscape(innerProxyTarget(t, body.URL)), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want 200: %s", w.Code, w.Body.String())
	}

	if !gotOK {
		t.Fatal("origin received no Authorization header")
	}
	if gotUser != "sub" || gotPass != "secret" {
		t.Errorf("origin got %q/%q, want sub/secret", gotUser, gotPass)
	}
	if strings.Contains(gotRequestURI, "sub") || strings.Contains(gotRequestURI, "secret") {
		t.Errorf("credentials reached the origin in the request line: %q", gotRequestURI)
	}
}

// innerProxyTarget unwraps the ?url= parameter of a proxy URL.
func innerProxyTarget(t *testing.T, proxyURL string) string {
	t.Helper()
	u, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatalf("parse proxy url %q: %v", proxyURL, err)
	}
	return u.Query().Get("url")
}
