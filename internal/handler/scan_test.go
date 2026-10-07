package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/scanner"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	"github.com/jmvbambico/adoboflix/internal/source/file"
)

// stubProbeSource is a Source that CAN enumerate its library's streams: it
// implements the optional source.StreamProbeLister capability, like the file
// adapter. Its targets are fixed per instance, so a test can tell from the
// report which source was actually probed.
type stubProbeSource struct {
	source.Source
	name    string
	targets []source.ProbeTarget
	listErr error
	lists   int32
}

func (s *stubProbeSource) Name() string { return s.name }

func (s *stubProbeSource) ListStreamsForProbe() ([]source.ProbeTarget, error) {
	atomic.AddInt32(&s.lists, 1)
	return s.targets, s.listErr
}

// stubNonProbeSource is a Source that CANNOT enumerate streams: it deliberately
// omits ListStreamsForProbe, exactly like adobotv-http, which only ever sees
// one subscriber's playlist.
type stubNonProbeSource struct {
	source.Source
	name string
}

func (s *stubNonProbeSource) Name() string { return s.name }

// aliveManifestServer serves a valid HLS manifest for every request, so probing
// any URL under it reports the stream alive. It exists only to keep the probes
// hermetic and fast.
func aliveManifestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func probeTarget(streamID, url string) source.ProbeTarget {
	return source.ProbeTarget{
		ChannelID:   "ch-" + streamID,
		ChannelName: "Chan " + streamID,
		StreamID:    streamID,
		Label:       streamID,
		URL:         url,
	}
}

func scanRouter(p *PlayerHandler) *gin.Engine {
	r := gin.New()
	r.POST("/api/v1/channels/scan", p.ScanChannels)
	r.GET("/api/v1/channels/scan/status", p.ScanStatus)
	r.GET("/api/v1/channels/scan/report", p.ScanReport)
	return r
}

type scanStatusEnvelope struct {
	Status scanner.Status `json:"status"`
}

func postScan(t *testing.T, r *gin.Engine) int {
	t.Helper()
	return doJSON(t, r, http.MethodPost, "/api/v1/channels/scan", "").Code
}

// waitForScanSettle polls /scan/status until the scan reaches a terminal state
// and returns its final status.
func waitForScanSettle(t *testing.T, r *gin.Engine) scanner.Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		w := doJSON(t, r, http.MethodGet, "/api/v1/channels/scan/status", "")
		if w.Code != http.StatusOK {
			t.Fatalf("scan status = %d, want 200: %s", w.Code, w.Body.String())
		}
		var env scanStatusEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode scan status: %v", err)
		}
		switch env.Status.State {
		case scanner.StateDone, scanner.StateError, scanner.StateCancelled:
			return env.Status
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not settle; last state = %q", env.Status.State)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func fetchScanReport(t *testing.T, r *gin.Engine) (*scanner.Report, int) {
	t.Helper()
	w := doJSON(t, r, http.MethodGet, "/api/v1/channels/scan/report", "")
	if w.Code != http.StatusOK {
		return nil, w.Code
	}
	var rep scanner.Report
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	return &rep, w.Code
}

func reportStreamIDs(rep *scanner.Report) map[string]bool {
	ids := map[string]bool{}
	for _, s := range rep.Streams {
		ids[s.StreamID] = true
	}
	return ids
}

// The permanent-501 bug: a scan while a non-capable source is active must not
// poison scanning for the rest of the process. After the user imports a local
// playlist — a scannable source — a scan must work.
func TestScanManagerRecoversAfterSwappingToACapableSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := aliveManifestServer(t)

	nonCapable := &stubNonProbeSource{name: adobotvhttp.Name}
	capable := &stubProbeSource{
		name:    file.Name,
		targets: []source.ProbeTarget{probeTarget("local-1", srv.URL+"/local/live.m3u8")},
	}

	p := NewPlayerHandler(nonCapable)
	r := scanRouter(p)

	if code := postScan(t, r); code != http.StatusNotImplemented {
		t.Fatalf("scan against a non-capable source = %d, want 501", code)
	}

	p.SwapSource(capable)

	if code := postScan(t, r); code != http.StatusAccepted {
		t.Fatalf("scan after swapping to a capable source = %d, want 202: the capability error must not be cached past its source", code)
	}
}

// The stale-results bug: a manager built for a capable source must not keep
// answering after the source is swapped for a non-capable one. Scanning must
// report 501, not serve the old source's results.
func TestScanAfterSwapToNonCapableReturns501NotStaleResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := aliveManifestServer(t)

	capable := &stubProbeSource{
		name:    file.Name,
		targets: []source.ProbeTarget{probeTarget("stale-1", srv.URL+"/stale/live.m3u8")},
	}
	p := NewPlayerHandler(capable)
	r := scanRouter(p)

	if code := postScan(t, r); code != http.StatusAccepted {
		t.Fatalf("first scan = %d, want 202", code)
	}
	waitForScanSettle(t, r)
	rep, code := fetchScanReport(t, r)
	if code != http.StatusOK {
		t.Fatalf("first report = %d, want 200", code)
	}
	if !reportStreamIDs(rep)["stale-1"] {
		t.Fatalf("first report = %+v, want the capable source's stream", rep.Streams)
	}

	p.SwapSource(&stubNonProbeSource{name: adobotvhttp.Name})

	if code := postScan(t, r); code != http.StatusNotImplemented {
		t.Fatalf("scan after swapping to a non-capable source = %d, want 501, not the old source's results", code)
	}
}

// Swapping to a DIFFERENT capable source must probe the new source's streams,
// not reuse the manager built for the previous one. Same adapter name, distinct
// pointer: identity, not name, is what selects the manager.
func TestScanAfterSwapToAnotherCapableSourceProbesTheNewSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := aliveManifestServer(t)

	srcA := &stubProbeSource{
		name:    file.Name,
		targets: []source.ProbeTarget{probeTarget("A-stream", srv.URL+"/a/live.m3u8")},
	}
	srcB := &stubProbeSource{
		name:    file.Name,
		targets: []source.ProbeTarget{probeTarget("B-stream", srv.URL+"/b/live.m3u8")},
	}

	p := NewPlayerHandler(srcA)
	r := scanRouter(p)

	if code := postScan(t, r); code != http.StatusAccepted {
		t.Fatalf("scan on source A = %d, want 202", code)
	}
	waitForScanSettle(t, r)
	rep, code := fetchScanReport(t, r)
	if code != http.StatusOK {
		t.Fatalf("report on source A = %d, want 200", code)
	}
	if !reportStreamIDs(rep)["A-stream"] {
		t.Fatalf("report on source A = %+v, want A's stream", rep.Streams)
	}

	p.SwapSource(srcB)

	if code := postScan(t, r); code != http.StatusAccepted {
		t.Fatalf("scan on source B = %d, want 202", code)
	}
	waitForScanSettle(t, r)
	rep, code = fetchScanReport(t, r)
	if code != http.StatusOK {
		t.Fatalf("report on source B = %d, want 200", code)
	}
	ids := reportStreamIDs(rep)
	if !ids["B-stream"] {
		t.Errorf("report on source B = %+v, want B's stream probed", rep.Streams)
	}
	if ids["A-stream"] {
		t.Errorf("report on source B = %+v, must not carry A's streams", rep.Streams)
	}
}

// A scan in flight when the source swaps must be cancelled, and its report must
// never be served as the new source's.
func TestScanInFlightWhenSourceSwapsIsCancelledAndItsReportDropped(t *testing.T) {
	gin.SetMode(gin.TestMode)

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFn := func() { releaseOnce.Do(func() { close(release) }) }
	cancelled := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			_, _ = io.WriteString(w, "#EXTM3U\n")
		case <-r.Context().Done():
			close(cancelled)
		}
	}))
	defer srv.Close()
	defer releaseFn()

	srcA := &stubProbeSource{
		name:    file.Name,
		targets: []source.ProbeTarget{probeTarget("A-inflight", srv.URL+"/a/live.m3u8")},
	}
	srcB := &stubProbeSource{
		name:    file.Name,
		targets: []source.ProbeTarget{probeTarget("B-fresh", srv.URL+"/b/live.m3u8")},
	}

	p := NewPlayerHandler(srcA)
	r := scanRouter(p)

	if code := postScan(t, r); code != http.StatusAccepted {
		t.Fatalf("scan on source A = %d, want 202", code)
	}
	// The probe is now in flight and blocked inside the server handler.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe never reached the server")
	}

	p.SwapSource(srcB)

	// The in-flight probe must be cancelled promptly by the swap.
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("the in-flight probe was not cancelled when the source swapped")
	}
	releaseFn()

	// A's report must not be served as B's: no scan has run for B yet.
	rep, code := fetchScanReport(t, r)
	if code == http.StatusOK {
		t.Fatalf("report after swap = 200 with %+v; the old source's report must not be served as the new source's", rep.Streams)
	}
	if code != http.StatusNotFound {
		t.Fatalf("report after swap = %d, want 404 (no report for the new source)", code)
	}
}
