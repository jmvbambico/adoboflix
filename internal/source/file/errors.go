package file

import "errors"

// The sentinels below are this adapter's failure vocabulary. They follow the
// shape adobotv-http established: a distinct error per actionable state, each
// wrapped with %w so callers can test with errors.Is, and each naming what the
// user should do next.
//
// Load-time failures are startup failures, not per-request ones. The file
// adapter reads its playlist once in New and answers every query from memory,
// so a bad path or bad JSON stops the server from booting rather than serving
// a silently empty library.
var (
	// ErrLibraryUnreadable is returned when the configured playlist path
	// cannot be read: it is missing, is a directory, or the process lacks
	// permission. An unreadable path is a startup error, never a silent empty
	// library.
	ErrLibraryUnreadable = errors.New("file: playlist file could not be read")

	// ErrMalformedLibrary is returned when the playlist bytes are not the
	// documented JSON envelope. This is reported as a startup error naming the
	// path and the parse failure, so a typo in a hand-authored file is
	// fixable rather than mysterious.
	ErrMalformedLibrary = errors.New("file: malformed playlist file")

	// ErrContentNotFound is returned when an opaque id does not resolve to an
	// item in the loaded library — or, for a channel, when it resolves but has
	// no playable stream. It is this adapter's not-found vocabulary, the same
	// role adobotvhttp.ErrContentNotFound plays for the HTTP adapter.
	ErrContentNotFound = errors.New("file: content not found in this playlist")
)
