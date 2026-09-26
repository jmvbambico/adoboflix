package scanner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
	alive, status, reason := ProbeStream(context.Background(), StreamToCheck{
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

	alive, _, _ := ProbeStream(context.Background(), StreamToCheck{URL: srv.URL})
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

	alive, status, _ := ProbeStream(context.Background(), StreamToCheck{URL: srv.URL + "/dash"})
	if !alive || status != 200 {
		t.Fatalf("DASH manifest: alive=%v status=%d, want alive with 200", alive, status)
	}

	alive, status, reason := ProbeStream(context.Background(), StreamToCheck{URL: srv.URL + "/html"})
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

	alive, status, reason := ProbeStream(context.Background(), StreamToCheck{URL: srv.URL})
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
	alive, status, reason := ProbeStream(context.Background(), StreamToCheck{URL: "http://127.0.0.1:1/manifest.m3u8"})
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

func TestProbeStreamHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	alive, _, reason := ProbeStream(ctx, StreamToCheck{URL: "http://127.0.0.1:1/manifest.m3u8"})
	if alive {
		t.Fatal("expected dead stream on cancelled context")
	}
	if reason == "" {
		t.Fatal("expected a reason")
	}
}

func TestReportFilenamesAndCSV(t *testing.T) {
	r := &Report{
		StartedAt: time.Date(2026, 9, 26, 15, 30, 45, 0, time.UTC),
		Streams: []ProbeResult{{
			ChannelName: "Test, Channel",
			Category:    "News",
			Label:       "main",
			URL:         "https://example.com/manifest.m3u8",
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
