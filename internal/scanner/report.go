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

var csvHeader = []string{
	"channel_name", "category", "label", "channel_id", "stream_id",
	"url", "result", "http_status", "reason", "probed_at",
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
		row := []string{
			s.ChannelName,
			s.Category,
			s.Label,
			s.ChannelID,
			s.StreamID,
			s.URL,
			result,
			strconv.Itoa(s.HTTPStatus),
			s.Reason,
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
