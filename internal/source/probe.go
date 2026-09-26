package source

import "fmt"

// ProbeTarget is what a stream-health probe needs, plus enough context to
// make the resulting report readable by a human: which channel and stream it
// is, what the stream is labelled, where it lives, and the per-stream headers
// several CDNs require on the request.
//
// The URL is the one place a resolvable stream address exists. It is handed to
// the probe and never serialised: the report carries only a redacted host and
// manifest (see internal/scanner.RedactStreamURL).
type ProbeTarget struct {
	ChannelID   string  `db:"channel_id"`
	ChannelName string  `db:"name"`
	Category    string  `db:"category"`
	StreamID    string  `db:"id"`
	Label       string  `db:"label"`
	URL         string  `db:"url"`
	UserAgent   *string `db:"user_agent"`
	Referer     *string `db:"referer"`
}

// StreamProbeLister is an OPTIONAL capability. A Source implements it when it
// can enumerate the whole library's streams for health probing. postgres-direct
// can, because it reads the library table directly. An HTTP adapter that only
// ever sees one user's playlist may not, and is not required to: scanning is a
// report about the operator's whole library, not about one subscriber's slice.
//
// Callers must type-assert for this capability and report a clear unsupported
// error when it is absent. It is deliberately not part of Source, so that
// every adapter is not forced to fake an enumeration it cannot perform.
type StreamProbeLister interface {
	// ListStreamsForProbe returns every stream in the library that can be
	// probed, with its channel metadata. It is read-only.
	ListStreamsForProbe() ([]ProbeTarget, error)
}

// UnsupportedScanError is the honest error a caller reports when the active
// source does not implement StreamProbeLister. It names the source and says
// plainly that scanning is unsupported for it, so the user sees why the scan
// endpoints are unavailable instead of a panic or a silently empty scan.
func UnsupportedScanError(name string) error {
	return fmt.Errorf("source %q does not support health scanning: it cannot enumerate the library's streams", name)
}
