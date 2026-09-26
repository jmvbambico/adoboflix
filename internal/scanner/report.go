package scanner

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"
)

// Filename returns a stable, human-friendly download name for the report,
// e.g. "adoboflix-scan-report-20260926-153045.json".
func (r *Report) Filename(ext string) string {
	if ext != "" && ext[0] != '.' {
		ext = "." + ext
	}
	return fmt.Sprintf("adoboflix-scan-report-%s%s", r.StartedAt.UTC().Format("20060102-150405"), ext)
}

// csvHeader mirrors the JSON report. It carries "host" and "manifest" rather
// than a "url" column: a downloadable report must never contain a resolvable
// stream URL, and a bare host makes "this entire host is down" obvious at a
// glance — which is the report's main job.
var csvHeader = []string{
	"channel_name", "category", "label", "channel_id", "stream_id",
	"host", "manifest", "result", "http_status", "reason", "probed_at",
}

// sanitizeCSVCell neutralises a spreadsheet formula in a free-text cell by
// prefixing an apostrophe. Excel, LibreOffice and Google Sheets treat a cell
// beginning with '=', '+', '-', '@', tab or carriage return as a formula, and
// this report is a file a human downloads and forwards over a chat channel —
// exactly the path that attack class targets. The fields it protects
// (channel names, categories, labels, ids, hosts, probe reasons) originate in
// AdoboTV's library, which is populated by importing third-party M3U
// playlists: data AdoboFlix does not control. The apostrophe makes the
// spreadsheet render the cell as literal text.
//
// This is applied ONLY to the CSV writer. JSON is a programmatic format and
// must carry the value verbatim; an injected apostrophe there would corrupt
// the data for any consumer.
func sanitizeCSVCell(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}

// WriteCSV serializes the report as CSV for download.
func WriteCSV(w io.Writer, r *Report) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}
	for _, s := range r.Streams {
		result := "dead"
		if s.Alive {
			result = "alive"
		}
		// Every cell that can carry library- or error-derived text is passed
		// through sanitizeCSVCell. The three generated cells — result,
		// http_status and probed_at — are formatted here and left alone, so a
		// negative numeric status is not mistaken for a formula and mangled.
		row := []string{
			sanitizeCSVCell(s.ChannelName),
			sanitizeCSVCell(s.Category),
			sanitizeCSVCell(s.Label),
			sanitizeCSVCell(s.ChannelID),
			sanitizeCSVCell(s.StreamID),
			sanitizeCSVCell(s.Host),
			sanitizeCSVCell(s.Manifest),
			result,
			strconv.Itoa(s.HTTPStatus),
			sanitizeCSVCell(s.Reason),
			s.ProbedAt.UTC().Format(time.RFC3339),
		}
		if err := cw.Write(row); err != nil {
			return fmt.Errorf("write csv row for stream %s: %w", s.StreamID, err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}
	return nil
}
