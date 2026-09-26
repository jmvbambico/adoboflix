// Package scanner probes stream health for its OWN user and produces an
// in-memory, downloadable report.
//
// It has NO database dependency of any kind — not even a handle it could
// choose not to use. Probe targets arrive as []source.ProbeTarget, enumerated
// by whichever source adapter implements the optional StreamProbeLister
// capability, and results are aggregated in memory. "The scanner never writes
// upstream" is therefore not a rule a reviewer has to audit: the package has
// no way to reach a database, so the type system enforces it.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
)

const (
	// ScanConcurrency bounds the number of parallel probe goroutines.
	ScanConcurrency = 20
	// ScanTimeout is the explicit timeout applied to every single probe.
	ScanTimeout = 10 * time.Second
	// scanBudget caps one full scan run.
	scanBudget = 5 * time.Minute
	// probeBodyBytes is how much of each response body is inspected.
	probeBodyBytes = 4096

	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// RedactStreamURL reduces a stream URL to the two things a report may carry:
// the host and the final path segment (the manifest). It drops the query
// string entirely — that is where auth tokens and DRM keys live — and every
// intermediate path segment.
//
//	https://la.drmlive.au/live/abc123/index.mpd?auth=SECRET
//	  -> host "la.drmlive.au", manifest "index.mpd"
//
// A URL that cannot be parsed, or that has no host, degrades to empty strings
// rather than falling back to the raw URL: a malformed input must never leak
// the very thing redaction exists to hide. Hostname() also strips any
// userinfo and port, so embedded credentials cannot ride along.
func RedactStreamURL(raw string) (host, manifest string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", ""
	}
	host = u.Hostname()
	manifest = path.Base(u.Path)
	if manifest == "/" || manifest == "." {
		manifest = ""
	}
	return host, manifest
}

// ProbeResult is one row of the report: everything a human needs to act on
// a dead stream without touching the database. It carries only the redacted
// host and manifest — never a resolvable URL.
type ProbeResult struct {
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	Category    string    `json:"category"`
	StreamID    string    `json:"stream_id"`
	Label       string    `json:"label"`
	Host        string    `json:"host"`
	Manifest    string    `json:"manifest"`
	Alive       bool      `json:"alive"`
	HTTPStatus  int       `json:"http_status,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	ProbedAt    time.Time `json:"probed_at"`
}

// Report is the aggregate of one scan run. It lives in memory only.
type Report struct {
	StartedAt     time.Time     `json:"started_at"`
	FinishedAt    time.Time     `json:"finished_at"`
	TotalChannels int           `json:"total_channels"`
	AliveChannels int           `json:"alive_channels"`
	DeadChannels  int           `json:"dead_channels"`
	TotalStreams  int           `json:"total_streams"`
	AliveStreams  int           `json:"alive_streams"`
	DeadStreams   int           `json:"dead_streams"`
	Streams       []ProbeResult `json:"streams"`
}

// probeErrorDetail returns the underlying cause of a transport error WITHOUT
// the *url.Error wrapper. That wrapper's Error() is "Get <full url>: <cause>",
// and the reason it produces ends up in the downloadable report — so using it
// verbatim would re-leak the very URL the report redacts. The cause alone
// (dial/DNS/TLS/timeout text) names what broke and carries no path or query.
func probeErrorDetail(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	return err.Error()
}

// classifyProbeError turns a transport failure into a human-readable reason
// that says WHAT kind of failure it was (timeout vs connection/TLS vs other),
// so a report never lumps a broken CDN handshake behind a generic message. It
// never embeds the probed URL — see probeErrorDetail.
func classifyProbeError(err error) string {
	detail := probeErrorDetail(err)
	if errors.Is(err, context.Canceled) {
		return "cancelled: " + detail
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return "timeout: " + detail
		}
		// Dial, DNS, certificate and TLS handshake failures all surface
		// through *url.Error here — name them as connection errors.
		return "connection error: " + detail
	}
	return "request failed: " + detail
}

// ProbeStream tests whether a stream target is alive. It classifies the stream
// by CONTENT (#EXTM3U, <MPD, …) rather than trusting the status code alone.
// When the stream row carries its own user_agent/referer, those are used —
// several CDNs reject probes that arrive with the wrong headers.
func ProbeStream(ctx context.Context, target source.ProbeTarget) (alive bool, httpStatus int, reason string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return false, 0, "invalid URL: " + err.Error()
	}

	ua := defaultUserAgent
	if target.UserAgent != nil && *target.UserAgent != "" {
		ua = *target.UserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	if target.Referer != nil && *target.Referer != "" {
		req.Header.Set("Referer", *target.Referer)
	}

	client := &http.Client{
		Timeout: ScanTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, 0, classifyProbeError(err)
	}
	defer resp.Body.Close()

	// Read first 4KB to verify content is real
	buf := make([]byte, probeBodyBytes)
	n, _ := resp.Body.Read(buf)

	statusOk := resp.StatusCode >= 200 && resp.StatusCode < 400

	// For HLS/DASH, content matters more than status code
	if n > 0 {
		content := string(buf[:n])
		isValidStream := false
		if strings.Contains(content, "#EXTM3U") {
			isValidStream = true // HLS manifest
		} else if strings.Contains(content, "<MPD") || strings.Contains(content, "MPD") {
			isValidStream = true // DASH manifest
		} else if strings.Contains(content, "m3u8") || strings.Contains(content, ".m3u8") {
			isValidStream = true // HLS playlist reference
		} else if strings.Contains(content, "mpd") || strings.Contains(content, ".mpd") {
			isValidStream = true // DASH reference
		} else if strings.Contains(content, "ism") || strings.Contains(content, ".ism") {
			isValidStream = true // Smooth Streaming
		} else if n > 100 {
			// Has significant content — might be a binary stream
			isValidStream = statusOk
		}
		if statusOk && isValidStream {
			return true, resp.StatusCode, ""
		}
		if !statusOk {
			return false, resp.StatusCode, fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
		return false, resp.StatusCode, fmt.Sprintf("HTTP %d: response does not look like a stream manifest", resp.StatusCode)
	}

	if !statusOk {
		return false, resp.StatusCode, fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return true, resp.StatusCode, ""
}

// ProgressFunc receives probe progress: streams probed, total, alive so far.
type ProgressFunc func(probed, total, alive int)

// RunScan probes every target, aggregates the results in memory and returns a
// report. It persists NOTHING: it holds no database handle, and its results
// carry no resolvable URL. Errors wrap with %w; context cancellation aborts
// the run.
func RunScan(ctx context.Context, targets []source.ProbeTarget, progress ProgressFunc) (*Report, error) {
	ctx, cancel := context.WithTimeout(ctx, scanBudget)
	defer cancel()

	report := &Report{
		StartedAt:    time.Now().UTC(),
		TotalStreams: len(targets),
	}
	if progress != nil {
		progress(0, len(targets), 0)
	}

	jobs := make(chan source.ProbeTarget, len(targets))
	results := make(chan ProbeResult, len(targets))

	// Bounded worker pool.
	var wg sync.WaitGroup
	for i := 0; i < ScanConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range jobs {
				host, manifest := RedactStreamURL(target.URL)
				result := ProbeResult{
					ChannelID:   target.ChannelID,
					ChannelName: target.ChannelName,
					Category:    target.Category,
					StreamID:    target.StreamID,
					Label:       target.Label,
					Host:        host,
					Manifest:    manifest,
					ProbedAt:    time.Now().UTC(),
				}
				if ctx.Err() != nil {
					result.Reason = "not probed: " + ctx.Err().Error()
					results <- result
					continue
				}
				result.Alive, result.HTTPStatus, result.Reason = ProbeStream(ctx, target)
				results <- result
			}
		}()
	}

	// The channel is buffered for the full target count, so enqueueing never
	// blocks; workers honor ctx per target instead.
	for _, t := range targets {
		jobs <- t
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	allChannels := map[string]struct{}{}
	aliveChannels := map[string]struct{}{}
	probed := 0
	aliveStreams := 0

	for r := range results {
		probed++
		allChannels[r.ChannelID] = struct{}{}
		if r.Alive {
			aliveStreams++
			aliveChannels[r.ChannelID] = struct{}{}
		}
		report.Streams = append(report.Streams, r)
		if progress != nil {
			progress(probed, len(targets), aliveStreams)
		}
	}

	report.FinishedAt = time.Now().UTC()
	report.TotalChannels = len(allChannels)
	report.AliveChannels = len(aliveChannels)
	report.DeadChannels = len(allChannels) - len(aliveChannels)
	report.AliveStreams = aliveStreams
	report.DeadStreams = len(targets) - aliveStreams

	// Stable, human-friendly order: channel name, then label, then stream id.
	sort.Slice(report.Streams, func(i, j int) bool {
		if report.Streams[i].ChannelName != report.Streams[j].ChannelName {
			return report.Streams[i].ChannelName < report.Streams[j].ChannelName
		}
		if report.Streams[i].Label != report.Streams[j].Label {
			return report.Streams[i].Label < report.Streams[j].Label
		}
		return report.Streams[i].StreamID < report.Streams[j].StreamID
	})

	if err := ctx.Err(); err != nil {
		return report, fmt.Errorf("scan stopped after %d/%d probes: %w", probed, len(targets), err)
	}
	return report, nil
}
