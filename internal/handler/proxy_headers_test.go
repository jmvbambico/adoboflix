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

// fabricatedUA is the default ProxyStream used to invent for every stream that
// configured none. Origins that filter on User-Agent read it as malformed and
// 403 it (the A2Z / ZTE JITP DRM report). It must appear nowhere now.
const fabricatedUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

const fabricatedReferer = "https://www.google.com"

// headerStubSource serves one fixed channel stream so a test can pin which
// optional per-stream headers resolve hands to the proxy.
type headerStubSource struct {
	source.Source
	stream *source.Stream
}

func (s *headerStubSource) Name() string { return "header-stub" }

func (s *headerStubSource) ResolveChannelStream(channelID string) (*source.Stream, error) {
	return s.stream, nil
}

// recordingOrigin returns an httptest origin and a pointer to the headers of
// the last request it received. What actually reached the origin is the
// assertion that matters — not what the handler meant to send.
func recordingOrigin(t *testing.T) (*httptest.Server, *http.Header) {
	t.Helper()
	var got http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = req.Header.Clone()
		w.Header().Set("Content-Type", "application/dash+xml")
		w.Write([]byte("<MPD/>"))
	}))
	t.Cleanup(origin.Close)
	return origin, &got
}

func newProxyRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/proxy", NewPlayerHandler(&headerStubSource{}).ProxyStream)
	return r
}

// proxyRequest drives the proxy endpoint. An empty incomingUA deletes the
// header so the request genuinely carries none.
func proxyRequest(t *testing.T, r *gin.Engine, q url.Values, incomingUA string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proxy?"+q.Encode(), nil)
	if incomingUA == "" {
		req.Header.Del("User-Agent")
	} else {
		req.Header.Set("User-Agent", incomingUA)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestProxyForwardsIncomingUserAgentWhenStreamHasNone is the core of Bug 1:
// with no `ua` param, the caller's own User-Agent is what reaches the origin,
// and the fabricated default does not.
func TestProxyForwardsIncomingUserAgentWhenStreamHasNone(t *testing.T) {
	origin, got := recordingOrigin(t)
	r := newProxyRouter()

	const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	w := proxyRequest(t, r, url.Values{"url": {origin.URL + "/live/stream.mpd"}}, browserUA)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	ua := got.Get("User-Agent")
	if ua != browserUA {
		t.Errorf("origin User-Agent = %q, want the caller's %q", ua, browserUA)
	}
	if ua == fabricatedUA {
		t.Errorf("origin received the fabricated default UA %q", fabricatedUA)
	}
}

// TestProxySendsNoUserAgentWhenNeitherIsPresent: no `ua` param and no incoming
// User-Agent means the origin gets none — not a fabricated string, and not
// net/http's own Go-http-client default either.
func TestProxySendsNoUserAgentWhenNeitherIsPresent(t *testing.T) {
	origin, got := recordingOrigin(t)
	r := newProxyRouter()

	w := proxyRequest(t, r, url.Values{"url": {origin.URL + "/live/stream.mpd"}}, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	if ua := got.Get("User-Agent"); ua != "" {
		t.Errorf("origin User-Agent = %q, want none", ua)
	}
}

// TestProxyForwardsExplicitUserAgentParamUnchanged preserves the existing
// behaviour for streams that do configure a UA.
func TestProxyForwardsExplicitUserAgentParamUnchanged(t *testing.T) {
	origin, got := recordingOrigin(t)
	r := newProxyRouter()

	const configuredUA = "ZTE-JITP-Player/2.3"
	w := proxyRequest(t, r, url.Values{
		"url": {origin.URL + "/live/stream.mpd"},
		"ua":  {configuredUA},
	}, "incoming/1.0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	if ua := got.Get("User-Agent"); ua != configuredUA {
		t.Errorf("origin User-Agent = %q, want the configured %q", ua, configuredUA)
	}
}

// TestProxyDoesNotFabricateReferer: with no `ref` and no per-source override,
// the origin receives no Referer at all.
func TestProxyDoesNotFabricateReferer(t *testing.T) {
	origin, got := recordingOrigin(t)
	r := newProxyRouter()

	w := proxyRequest(t, r, url.Values{"url": {origin.URL + "/live/stream.mpd"}}, "incoming/1.0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	if ref := got.Get("Referer"); ref != "" {
		t.Errorf("origin Referer = %q, want none", ref)
	}
	if ref := got.Get("Referer"); ref == fabricatedReferer {
		t.Errorf("origin received the fabricated default Referer %q", fabricatedReferer)
	}
}

// TestProxyKeepsPerSourceReferer pins the deliberate per-provider overrides:
// these are requirements, not fabrications, and VOD playback depends on them.
func TestProxyKeepsPerSourceReferer(t *testing.T) {
	cases := map[string]string{
		"vidstreaming": "https://vidstreaming.io",
		"vidcloud9":    "https://vidcloud9.com",
		"alionscience": "https://alionscience.com",
		"miruro":       "https://www.miruro.tv",
		"vixsrc":       "https://vixcloud.com",
	}
	for sourceName, want := range cases {
		t.Run(sourceName, func(t *testing.T) {
			origin, got := recordingOrigin(t)
			r := newProxyRouter()

			w := proxyRequest(t, r, url.Values{
				"url":    {origin.URL + "/live/stream.mpd"},
				"source": {sourceName},
			}, "incoming/1.0")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}

			if ref := got.Get("Referer"); ref != want {
				t.Errorf("origin Referer = %q, want provider Referer %q", ref, want)
			}
		})
	}
}

// TestResolveChannelStreamCarriesConfiguredHeaders is Bug 2: a live stream row
// with a UA and Referer must forward both, not drop them.
func TestResolveChannelStreamCarriesConfiguredHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ua := "chan-player/1.0"
	ref := "https://provider.example/"
	h := NewPlayerHandler(&headerStubSource{stream: &source.Stream{
		ID: "s1", ChannelID: "c1", URL: "https://cdn.example.com/live/a.mpd",
		SourceType: "zte", UserAgent: &ua, Referer: &ref,
	}})
	r := gin.New()
	r.GET("/api/v1/channels/:id/resolve", h.ResolveChannelStream)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/channels/c1/resolve", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	u := decodeResolveURL(t, w.Body.Bytes())
	if got := u.Query().Get("ua"); got != ua {
		t.Errorf("proxy url ua = %q, want %q", got, ua)
	}
	if got := u.Query().Get("ref"); got != ref {
		t.Errorf("proxy url ref = %q, want %q", got, ref)
	}
}

// TestResolveChannelStreamOmitsAbsentHeaders: a row with NULL UA and Referer
// must append neither param (the A2Z shape).
func TestResolveChannelStreamOmitsAbsentHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewPlayerHandler(&headerStubSource{stream: &source.Stream{
		ID: "s2", ChannelID: "c2", URL: "https://cdn.example.com/live/b.mpd",
		SourceType: "zte",
	}})
	r := gin.New()
	r.GET("/api/v1/channels/:id/resolve", h.ResolveChannelStream)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/channels/c2/resolve", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	u := decodeResolveURL(t, w.Body.Bytes())
	if _, ok := u.Query()["ua"]; ok {
		t.Error("proxy url carries a ua param for a stream with no User-Agent")
	}
	if _, ok := u.Query()["ref"]; ok {
		t.Error("proxy url carries a ref param for a stream with no Referer")
	}
}

func decodeResolveURL(t *testing.T, body []byte) *url.URL {
	t.Helper()
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode resolve body: %v", err)
	}
	if resp.URL == "" {
		t.Fatal("resolve body carried no url")
	}
	u, err := url.Parse(resp.URL)
	if err != nil {
		t.Fatalf("parse proxy url %q: %v", resp.URL, err)
	}
	if !strings.HasPrefix(resp.URL, "/api/v1/proxy") && !strings.Contains(resp.URL, "/api/v1/proxy?") {
		t.Errorf("resolve url %q is not a proxy url", resp.URL)
	}
	return u
}
