package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// failingLister is the source capability when enumeration itself fails; the
// manager must surface that as an error state, never a panic or a silent
// empty scan.
type failingLister struct{ err error }

func (f failingLister) ListStreamsForProbe() ([]source.ProbeTarget, error) { return nil, f.err }

func TestManagerRecordsListerFailure(t *testing.T) {
	m := NewManager(failingLister{err: errors.New("upstream unavailable")})
	if _, started := m.Start(); !started {
		t.Fatal("expected the scan to start")
	}

	deadline := time.After(2 * time.Second)
	for {
		st := m.Status()
		if st.State != StateRunning {
			if st.State != StateError {
				t.Fatalf("state = %q, want %q", st.State, StateError)
			}
			if !strings.Contains(st.Error, "upstream unavailable") {
				t.Errorf("error %q does not carry the lister failure", st.Error)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("scan never left the running state")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestProbeStreamUsesPerStreamHeaders(t *testing.T) {
	var gotUA, gotReferer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotReferer = r.Header.Get("Referer")
		w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n"))
	}))
	defer srv.Close()

	ua := "ExoPlayerDemo/2.15.1 (Linux; Android 13) ExoPlayerLib/2.15.1"
	ref := "https://example.com/"
	alive, status, reason := ProbeStream(context.Background(), source.ProbeTarget{
		URL:       srv.URL,
		UserAgent: &ua,
		Referer:   &ref,
	})
	if !alive {
		t.Fatalf("expected alive stream, got status=%d reason=%q", status, reason)
	}
	if gotUA != ua {
		t.Errorf("User-Agent = %q, want per-stream %q", gotUA, ua)
	}
	if gotReferer != ref {
		t.Errorf("Referer = %q, want per-stream %q", gotReferer, ref)
	}
}

func TestProbeStreamDefaultsWhenNoPerStreamHeaders(t *testing.T) {
	var gotUA, gotReferer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotReferer = r.Header.Get("Referer")
		w.Write([]byte("#EXTM3U\n"))
	}))
	defer srv.Close()

	alive, _, _ := ProbeStream(context.Background(), source.ProbeTarget{URL: srv.URL})
	if !alive {
		t.Fatal("expected alive stream")
	}
	if !strings.Contains(gotUA, "Mozilla") {
		t.Errorf("User-Agent = %q, want default browser UA", gotUA)
	}
	if gotReferer != "" {
		t.Errorf("Referer = %q, want empty when stream has none", gotReferer)
	}
}

func TestProbeStreamClassifiesByContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/html" {
			w.Write([]byte("<html><body>goes here for more than one hundred bytes of error page content</body></html>"))
			return
		}
		w.Write([]byte("<MPD xmlns=\"urn:mpeg:dash:schema:mpd:2011\"></MPD>"))
	}))
	defer srv.Close()

	alive, status, _ := ProbeStream(context.Background(), source.ProbeTarget{URL: srv.URL + "/dash"})
	if !alive || status != 200 {
		t.Fatalf("DASH manifest: alive=%v status=%d, want alive with 200", alive, status)
	}

	alive, status, reason := ProbeStream(context.Background(), source.ProbeTarget{URL: srv.URL + "/html"})
	if alive {
		t.Fatalf("HTML error page: expected dead, got alive (status %d)", status)
	}
	if reason == "" {
		t.Fatal("expected a reason for a dead stream")
	}
}

func TestProbeStreamReportsHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	alive, status, reason := ProbeStream(context.Background(), source.ProbeTarget{URL: srv.URL})
	if alive {
		t.Fatal("expected dead stream on 403")
	}
	if status != http.StatusForbidden {
		t.Errorf("http status = %d, want 403", status)
	}
	if !strings.Contains(reason, "HTTP 403") {
		t.Errorf("reason = %q, want it to name HTTP 403", reason)
	}
}

func TestProbeStreamClassifiesConnectionError(t *testing.T) {
	// Port 1 on localhost refuses connections instantly.
	alive, status, reason := ProbeStream(context.Background(), source.ProbeTarget{URL: "http://127.0.0.1:1/manifest.m3u8"})
	if alive {
		t.Fatal("expected dead stream on refused connection")
	}
	if status != 0 {
		t.Errorf("http status = %d, want 0 (no response)", status)
	}
	if !strings.Contains(reason, "connection error") {
		t.Errorf("reason = %q, want it named a connection error", reason)
	}
}

func TestProbeStreamFailureReasonNeverContainsURL(t *testing.T) {
	// A refused connection surfaces through *url.Error, whose Error() embeds
	// the full URL. The classified reason must keep the kind of failure but
	// never the path or query — it lands in the downloadable report.
	const raw = "http://127.0.0.1:1/secret/path/manifest.m3u8?token=SUPERSECRET"
	alive, _, reason := ProbeStream(context.Background(), source.ProbeTarget{URL: raw})
	if alive {
		t.Fatal("expected dead stream on refused connection")
	}
	for _, leak := range []string{"SUPERSECRET", "token=", "/secret/path", raw} {
		if strings.Contains(reason, leak) {
			t.Errorf("reason leaks %q: %q", leak, reason)
		}
	}
	if !strings.Contains(reason, "connection error") {
		t.Errorf("reason = %q, want it to keep its classification", reason)
	}
}

func TestProbeStreamHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	alive, _, reason := ProbeStream(ctx, source.ProbeTarget{URL: "http://127.0.0.1:1/manifest.m3u8"})
	if alive {
		t.Fatal("expected dead stream on cancelled context")
	}
	if reason == "" {
		t.Fatal("expected a reason")
	}
}

func TestRedactStreamURL(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantHost     string
		wantManifest string
	}{
		{
			name:         "query string is dropped",
			raw:          "https://la.drmlive.au/live/abc123/index.mpd?auth=SECRET",
			wantHost:     "la.drmlive.au",
			wantManifest: "index.mpd",
		},
		{
			name:         "intermediate path segments are dropped",
			raw:          "https://cdn.example.com/a/b/c/main.m3u8",
			wantHost:     "cdn.example.com",
			wantManifest: "main.m3u8",
		},
		{
			name:         "userinfo and port are stripped",
			raw:          "https://user:pass@host.example.com:8443/x/y/index.mpd",
			wantHost:     "host.example.com",
			wantManifest: "index.mpd",
		},
		{
			name:         "malformed url degrades to empty",
			raw:          "://not-a-url/x.mpd?auth=SECRET",
			wantHost:     "",
			wantManifest: "",
		},
		{
			name:         "hostless url degrades to empty",
			raw:          "not-a-url/index.mpd",
			wantHost:     "",
			wantManifest: "",
		},
		{
			name:         "empty url degrades to empty",
			raw:          "",
			wantHost:     "",
			wantManifest: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, manifest := RedactStreamURL(tt.raw)
			if host != tt.wantHost || manifest != tt.wantManifest {
				t.Errorf("RedactStreamURL(%q) = (%q, %q), want (%q, %q)",
					tt.raw, host, manifest, tt.wantHost, tt.wantManifest)
			}
		})
	}
}

func TestReportNeverCarriesAResolvableURL(t *testing.T) {
	const raw = "https://la.drmlive.au/live/abc123/index.mpd?auth=SUPERSECRET"
	host, manifest := RedactStreamURL(raw)
	report := &Report{
		StartedAt:  time.Now().UTC(),
		FinishedAt: time.Now().UTC(),
		Streams: []ProbeResult{{
			ChannelName: "News HD",
			Category:    "News",
			Label:       "main",
			ChannelID:   "ch-1",
			StreamID:    "st-1",
			Host:        host,
			Manifest:    manifest,
			Alive:       false,
			HTTPStatus:  http.StatusForbidden,
			Reason:      "HTTP 403 Forbidden",
			ProbedAt:    time.Now().UTC(),
		}},
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, leak := range []string{"SUPERSECRET", "abc123", raw, "?auth="} {
		if strings.Contains(string(data), leak) {
			t.Errorf("JSON report leaks %q: %s", leak, data)
		}
	}
	if !strings.Contains(string(data), `"host":"la.drmlive.au"`) || !strings.Contains(string(data), `"manifest":"index.mpd"`) {
		t.Errorf("JSON report missing redacted host/manifest: %s", data)
	}

	var csvOut strings.Builder
	if err := WriteCSV(&csvOut, report); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	out := csvOut.String()
	for _, leak := range []string{"SUPERSECRET", "abc123", "https://", "?auth="} {
		if strings.Contains(out, leak) {
			t.Errorf("CSV report leaks %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, "host,manifest") {
		t.Errorf("CSV header missing host,manifest columns: %q", out)
	}
}

func TestReportFilenamesAndCSV(t *testing.T) {
	r := &Report{
		StartedAt: time.Date(2026, 9, 26, 15, 30, 45, 0, time.UTC),
		Streams: []ProbeResult{{
			ChannelName: "Test, Channel",
			Category:    "News",
			Label:       "main",
			Host:        "example.com",
			Manifest:    "index.mpd",
			Alive:       false,
			HTTPStatus:  404,
			Reason:      "HTTP 404 Not Found",
			ProbedAt:    time.Date(2026, 9, 26, 15, 30, 46, 0, time.UTC),
		}},
	}
	if got := r.Filename(".json"); got != "adoboflix-scan-report-20260926-153045.json" {
		t.Errorf("Filename = %q", got)
	}
	var sb strings.Builder
	if err := WriteCSV(&sb, r); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "channel_name,category,label") {
		t.Error("missing CSV header")
	}
	if !strings.Contains(out, `"Test, Channel"`) {
		t.Errorf("channel name not quoted: %q", out)
	}
	if !strings.Contains(out, "dead,404") {
		t.Errorf("missing result/status: %q", out)
	}
}
