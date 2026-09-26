package scanner

import (
	"context"
	"encoding/csv"
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

// freeTextColumns names the CSV columns that can carry library-derived or
// error-derived text, so every one of them must be formula-sanitised.
var freeTextColumns = []string{
	"channel_name", "category", "label", "channel_id",
	"stream_id", "host", "manifest", "reason",
}

// formulaReport is a report whose free-text cells all carry the given value.
func formulaReport(v string) *Report {
	return &Report{
		StartedAt:  time.Now().UTC(),
		FinishedAt: time.Now().UTC(),
		Streams: []ProbeResult{{
			ChannelName: v,
			Category:    v,
			Label:       v,
			ChannelID:   v,
			StreamID:    v,
			Host:        v,
			Manifest:    v,
			Reason:      v,
			ProbedAt:    time.Now().UTC(),
		}},
	}
}

// parseCSV re-reads a rendered report so assertions see the cell values a
// spreadsheet would, not the raw quoted text.
func parseCSV(t *testing.T, out string) (header []string, row []string) {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("re-read csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d csv rows, want header + 1 data row", len(rows))
	}
	return rows[0], rows[1]
}

func columnIndex(header []string) map[string]int {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i
	}
	return idx
}

func TestWriteCSVSanitisesFormulaCells(t *testing.T) {
	cases := []struct{ name, value string }{
		{"equals", "=cmd|'/c calc'!A0"},
		{"plus", "+1+1"},
		{"minus", "-2+3"},
		{"at", "@SUM(A1:A9)"},
		{"tab", "\t=hidden"},
		{"carriage return", "\r=hidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			if err := WriteCSV(&sb, formulaReport(tc.value)); err != nil {
				t.Fatalf("WriteCSV: %v", err)
			}
			header, row := parseCSV(t, sb.String())
			idx := columnIndex(header)
			for _, col := range freeTextColumns {
				if got, want := row[idx[col]], "'"+tc.value; got != want {
					t.Errorf("%s cell = %q, want %q", col, got, want)
				}
			}
			// result/http_status/probed_at are formatted by our own code and
			// must not be mistaken for formulas and mangled.
			if got := row[idx["http_status"]]; got != "0" {
				t.Errorf("http_status = %q, want unmodified 0", got)
			}
			if got := row[idx["result"]]; got != "dead" {
				t.Errorf("result = %q, want unmodified dead", got)
			}
		})
	}
}

func TestWriteCSVLeavesNormalCellsUntouched(t *testing.T) {
	r := formulaReport("News HD")
	r.Streams[0].HTTPStatus = 403
	r.Streams[0].Reason = "HTTP 403 Forbidden"

	var sb strings.Builder
	if err := WriteCSV(&sb, r); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	header, row := parseCSV(t, sb.String())
	idx := columnIndex(header)

	for _, col := range []string{"channel_name", "category", "label", "channel_id", "stream_id", "host", "manifest"} {
		if got := row[idx[col]]; got != "News HD" {
			t.Errorf("%s = %q, want %q untouched", col, got, "News HD")
		}
	}
	if got := row[idx["reason"]]; got != "HTTP 403 Forbidden" {
		t.Errorf("reason = %q, want it untouched", got)
	}
}

// TestFormulaSanitisationLeavesJSONVerbatim proves the defence is confined to
// the CSV writer: the JSON report is a programmatic format and must round-trip
// the original value with no injected apostrophe.
func TestFormulaSanitisationLeavesJSONVerbatim(t *testing.T) {
	const payload = `=HYPERLINK("http://evil.example")`
	r := formulaReport(payload)

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if strings.Contains(string(data), "'"+payload) {
		t.Errorf("JSON report received an injected apostrophe: %s", data)
	}

	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	got := decoded.Streams[0]
	for _, v := range []string{got.ChannelName, got.Category, got.Label, got.ChannelID, got.StreamID, got.Host, got.Manifest, got.Reason} {
		if v != payload {
			t.Errorf("JSON round-trip changed a value: got %q, want %q", v, payload)
		}
	}

	// CSV, by contrast, is sanitised — the apostrophe is spreadsheet-only.
	var sb strings.Builder
	if err := WriteCSV(&sb, r); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	_, row := parseCSV(t, sb.String())
	if row[0] != "'"+payload {
		t.Errorf("CSV channel_name = %q, want %q", row[0], "'"+payload)
	}
}

// staticLister returns a fixed target set without touching a database.
type staticLister struct{ targets []source.ProbeTarget }

func (s staticLister) ListStreamsForProbe() ([]source.ProbeTarget, error) { return s.targets, nil }

// panicLister panics during enumeration to exercise run()'s recover path.
type panicLister struct{}

func (panicLister) ListStreamsForProbe() ([]source.ProbeTarget, error) {
	panic("lister exploded")
}

// waitForTerminal blocks until the manager leaves the running state.
func waitForTerminal(t *testing.T, m *Manager) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := m.Status(); st.State != StateRunning {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("scan never left the running state")
	return Status{}
}

// TestManagerCancelEndsCancelledWithoutPublishingPartialReport covers the
// shutdown path: Cancel ends the scan in StateCancelled and leaves the
// truncated run unpublished, so Report() can never hand back a partial scan as
// if it were complete.
func TestManagerCancelEndsCancelledWithoutPublishingPartialReport(t *testing.T) {
	// A server that never answers on its own: the probe only ends once the
	// scan context is cancelled, which is exactly what Cancel does.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	targets := make([]source.ProbeTarget, 500)
	for i := range targets {
		targets[i] = source.ProbeTarget{URL: srv.URL, ChannelID: "ch-1", ChannelName: "News HD"}
	}

	m := NewManager(staticLister{targets: targets})
	if _, started := m.Start(); !started {
		t.Fatal("expected the scan to start")
	}
	m.Cancel()

	st := waitForTerminal(t, m)
	if st.State != StateCancelled {
		t.Fatalf("state = %q, want %q", st.State, StateCancelled)
	}
	if st.HasReport {
		t.Error("a cancelled scan published its partial report")
	}
	if _, err := m.Report(); !errors.Is(err, ErrNoReport) {
		t.Errorf("Report() error = %v, want ErrNoReport", err)
	}
}

// TestManagerCancelWithoutStartIsNoop covers the never-started case: a scan
// that never began must be cancellable without a panic and stay idle.
func TestManagerCancelWithoutStartIsNoop(t *testing.T) {
	m := NewManager(staticLister{})
	m.Cancel()
	if st := m.Status(); st.State != StateIdle {
		t.Errorf("state = %q, want %q", st.State, StateIdle)
	}
}

// TestManagerRecoverLeavesTerminalErrorAndRestartable proves a panic during a
// run cannot wedge the state machine in StateRunning: it lands in the terminal
// StateError, and the single-scan lock is released so a later Start succeeds.
func TestManagerRecoverLeavesTerminalErrorAndRestartable(t *testing.T) {
	m := NewManager(panicLister{})
	if _, started := m.Start(); !started {
		t.Fatal("expected the scan to start")
	}

	st := waitForTerminal(t, m)
	if st.State != StateError {
		t.Fatalf("state = %q, want terminal %q (panic must not wedge running)", st.State, StateError)
	}
	if !strings.Contains(st.Error, "panicked") {
		t.Errorf("error = %q, want it to record the panic", st.Error)
	}

	if _, started := m.Start(); !started {
		t.Fatal("manager wedged after a panic: a later scan could not start")
	}
	// Let the second (also panicking) run finish so it does not outlive the test.
	if st := waitForTerminal(t, m); st.State != StateError {
		t.Fatalf("restart state = %q, want %q", st.State, StateError)
	}
}
