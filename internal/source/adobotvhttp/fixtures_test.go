package adobotvhttp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	testPlaylistCode = "TESTCODE1234"
	testToken        = "tok-AbC123"
	// testDecoyURL is the value AdoboTV ships in channels[].url (the
	// channel_url_placeholder setting). It must never become playable.
	testDecoyURL = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
)

// newTestServer starts an httptest server, wires an Adapter at its URL, and
// returns both. The adapter's HTTP client keeps an explicit timeout even
// though it borrows the test transport.
func newTestServer(t *testing.T, ttl time.Duration, handler http.HandlerFunc) (*Adapter, *httptest.Server) {
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
	})
	if err != nil {
		t.Fatalf("newWithConfig: %v", err)
	}
	return adapter, srv
}

// abs builds an absolute URL pointing at the test server, using the request
// host so fixture URLs are correct without knowing the server URL up front.
func abs(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

func writeBody(w http.ResponseWriter, status int, contentType, body string) {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func writeErrorEnvelope(w http.ResponseWriter, status int, message string) {
	writeBody(w, status, "application/json", fmt.Sprintf(`{"success":false,"message":%q}`, message))
}

// envelopeBody renders a well-formed playlist envelope whose EPG and VOD
// library URLs point back at the test server. When channels is nil the default
// two-channel fixture is used.
func envelopeBody(t *testing.T, r *http.Request, channels []map[string]any) []byte {
	t.Helper()
	if channels == nil {
		channels = defaultChannels(r)
	}
	env := map[string]any{
		"provider": map[string]any{
			"user_message": "Welcome to AdoboTV tester!",
			"epg":          abs(r, "/v1/epg/adobotv.xml.gz?token="+testToken),
			"vod_library":  abs(r, "/v1/vod?token="+testToken),
			"billed_till":  "1893456000",
		},
		"categories": map[string]any{
			"movies": map[string]any{"name": "Movies", "icon": "https://img/m.png", "adult": false},
			"news":   map[string]any{"name": "News", "icon": "https://img/n.png", "adult": false},
		},
		"channels": channels,
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return body
}

func defaultChannels(r *http.Request) []map[string]any {
	return []map[string]any{
		{
			"name":             "Alpha TV",
			"category":         "movies",
			"icon":             "https://img/a.png",
			"epg_id":           "alpha.tvg",
			"url":              testDecoyURL,
			"runtime_attr_url": abs(r, "/v1/drm/key/ch-alpha?token="+testToken),
		},
		{
			"name":             "Beta News",
			"category":         "news",
			"icon":             "https://img/b.png",
			"epg_id":           "beta.tvg",
			"url":              testDecoyURL,
			"runtime_attr_url": abs(r, "/v1/drm/key/ch-beta?token="+testToken),
		},
	}
}

// drmRouter serves a canned body per /v1/drm/key/<tag>.
func drmRouter(bodies map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tag := strings.TrimPrefix(r.URL.Path, "/v1/drm/key/")
		body, ok := bodies[tag]
		if !ok {
			writeErrorEnvelope(w, http.StatusNotFound, "unknown content")
			return
		}
		writeBody(w, http.StatusOK, "text/plain; charset=utf-8", body)
	}
}

// libraryHandler serves a complete playlist — envelope, VOD library, DRM
// leaves and EPG — so the Source methods can be exercised end to end. The VOD
// assets are built per request so their runtime_attr_url values can point at
// the live test server.
func libraryHandler(t *testing.T, vod func(*http.Request) []map[string]any, drmBodies map[string]string, epg []byte) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/playlist/"+testPlaylistCode:
			writeBody(w, http.StatusOK, "application/json", string(envelopeBody(t, r, nil)))
		case r.URL.Path == "/v1/vod":
			assets := []map[string]any{}
			if vod != nil {
				assets = vod(r)
			}
			body, err := json.Marshal(assets)
			if err != nil {
				t.Errorf("marshal vod fixtures: %v", err)
			}
			writeBody(w, http.StatusOK, "application/json", string(body))
		case strings.HasPrefix(r.URL.Path, "/v1/drm/key/"):
			drmRouter(drmBodies)(w, r)
		case strings.HasPrefix(r.URL.Path, "/v1/epg/"):
			writeBody(w, http.StatusOK, "application/gzip", string(epg))
		default:
			writeErrorEnvelope(w, http.StatusNotFound, "unexpected path "+r.URL.Path)
		}
	}
}

// drmBody builds the documented /v1/drm/key/ body: base64 of ObfuscateJSON of
// the details. The obfuscation is faithful — keys and values are emitted as
// \uXXXX escapes while JSON punctuation is preserved — so every test that uses
// this also proves encoding/json reads it without a de-obfuscator.
func drmBody(fields map[string]string) string {
	return base64.StdEncoding.EncodeToString([]byte(obfuscateJSON(fields)))
}

func obfuscateJSON(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + unicodeEscape(k) + `":"` + unicodeEscape(fields[k]) + `"`)
	}
	b.WriteString("}")
	return b.String()
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(body)
}

func unicodeEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return b.String()
}

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
