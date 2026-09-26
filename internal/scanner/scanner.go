// Package scanner probes stream health for its OWN user and produces an
// in-memory, downloadable report. It is strictly read-only against the
// AdoboTV database: SELECTs only. Probe results are never persisted upstream
// (no check_status, no last_check, no playlist writes) — the user hands the
// report to the operator out of band.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
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

// StreamToCheck holds the data needed to test a stream.
type StreamToCheck struct {
	ChannelID   string  `db:"channel_id"`
	StreamID    string  `db:"id"`
	Label       string  `db:"label"`
	URL         string  `db:"url"`
	SourceType  string  `db:"source_type"`
	ChannelName string  `db:"name"`
	Category    string  `db:"category"`
	UserAgent   *string `db:"user_agent"`
	Referer     *string `db:"referer"`
}

// ProbeResult is one row of the report: everything a human needs to act on
// a dead stream without touching the database.
type ProbeResult struct {
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	Category    string    `json:"category"`
	StreamID    string    `json:"stream_id"`
	Label       string    `json:"label"`
	URL         string    `json:"url"`
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

// streamColumns lists the columns we explicitly select. Selecting "*" breaks
// against the AdoboTV schema because it carries columns this struct does not
// map, and sqlx errors on unmapped destinations.
const streamColumns = `s.channel_id, s.id, COALESCE(s.label, '') AS label, s.url,
	s.source_type, s.user_agent, s.referer,
	c.name, COALESCE(c.category, 'Unknown') AS category`

// FetchAllStreams returns all streams with their channel metadata for probing.
// Read-only: a SELECT against streams joined with channels.
func FetchAllStreams(database *sqlx.DB) ([]StreamToCheck, error) {
	var streams []StreamToCheck
	query := `
		SELECT ` + streamColumns + `
		FROM streams s
		JOIN channels c ON c.id = s.channel_id
		WHERE s.url IS NOT NULL AND s.url != ''
		ORDER BY c.name, s.is_default DESC
	`
	if err := database.Select(&streams, query); err != nil {
		return nil, fmt.Errorf("fetch streams: %w", err)
	}
	return streams, nil
}

// classifyProbeError turns a transport failure into a human-readable reason
// that says WHAT kind of failure it was (timeout vs connection/TLS vs other),
// so a report never lumps a broken CDN handshake behind a generic message.
func classifyProbeError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled: " + err.Error()
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return "timeout: " + err.Error()
		}
		// Dial, DNS, certificate and TLS handshake failures all surface
		// through *url.Error here — name them as connection errors.
		return "connection error: " + err.Error()
	}
	return "request failed: " + err.Error()
}

// ProbeStream tests whether a stream URL is alive. It classifies the stream
// by CONTENT (#EXTM3U, <MPD, …) rather than trusting the status code alone.
// When the stream row carries its own user_agent/referer, those are used —
// several CDNs reject probes that arrive with the wrong headers.
func ProbeStream(ctx context.Context, stream StreamToCheck) (alive bool, httpStatus int, reason string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stream.URL, nil)
	if err != nil {
		return false, 0, "invalid URL: " + err.Error()
	}

	ua := defaultUserAgent
	if stream.UserAgent != nil && *stream.UserAgent != "" {
		ua = *stream.UserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	if stream.Referer != nil && *stream.Referer != "" {
		req.Header.Set("Referer", *stream.Referer)
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

// RunScan probes every stream, aggregates the results in memory and returns a
// report. It persists NOTHING upstream — no check_status, no last_check, no
// playlist writes. Errors wrap with %w; context cancellation aborts the run.
func RunScan(ctx context.Context, database *sqlx.DB, progress ProgressFunc) (*Report, error) {
	streams, err := FetchAllStreams(database)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, scanBudget)
	defer cancel()

	report := &Report{
		StartedAt:  time.Now().UTC(),
		TotalStreams: len(streams),
	}
	if progress != nil {
		progress(0, len(streams), 0)
	}

	jobs := make(chan StreamToCheck, len(streams))
	results := make(chan ProbeResult, len(streams))

	// Bounded worker pool.
	var wg sync.WaitGroup
	for i := 0; i < ScanConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for stream := range jobs {
				result := ProbeResult{
					ChannelID:   stream.ChannelID,
					ChannelName: stream.ChannelName,
					Category:    stream.Category,
					StreamID:    stream.StreamID,
					Label:       stream.Label,
					URL:         stream.URL,
					ProbedAt:    time.Now().UTC(),
				}
				if ctx.Err() != nil {
					result.Reason = "not probed: " + ctx.Err().Error()
					results <- result
					continue
				}
				result.Alive, result.HTTPStatus, result.Reason = ProbeStream(ctx, stream)
				results <- result
			}
		}()
	}

	// The channel is buffered for the full stream count, so enqueueing never
	// blocks; workers honor ctx per stream instead.
	for _, s := range streams {
		jobs <- s
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
			progress(probed, len(streams), aliveStreams)
		}
	}

	report.FinishedAt = time.Now().UTC()
	report.TotalChannels = len(allChannels)
	report.AliveChannels = len(aliveChannels)
	report.DeadChannels = len(allChannels) - len(aliveChannels)
	report.AliveStreams = aliveStreams
	report.DeadStreams = len(streams) - aliveStreams

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
		return report, fmt.Errorf("scan stopped after %d/%d probes: %w", probed, len(streams), err)
	}
	return report, nil
}
