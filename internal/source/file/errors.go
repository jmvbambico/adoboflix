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

	// ErrMalformedLibrary is returned when the playlist bytes do not parse as
	// the format their extension selected: not the documented JSON envelope for
	// a .json file, not an M3U playlist for a .m3u/.m3u8 file. This is reported
	// as a startup error naming the path and the parse failure, so a typo in a
	// hand-authored file is fixable rather than mysterious.
	//
	// The format is chosen by extension and never re-chosen on failure: a
	// malformed .m3u reports as a malformed M3U, and is never silently
	// re-parsed as JSON.
	ErrMalformedLibrary = errors.New("file: malformed playlist file")

	// ErrUnsupportedFormat is returned when the playlist path's extension is
	// neither .json nor .m3u/.m3u8. Selecting a parser by content-sniffing
	// would be ambiguous — a body may be valid twice, or deliberately look like
	// the wrong format — so the extension decides, and an extension this
	// adapter does not know is a startup error naming the supported set.
	ErrUnsupportedFormat = errors.New("file: unsupported playlist format")

	// ErrContentNotFound is returned when an opaque id does not resolve to an
	// item in the loaded library — or, for a channel, when it resolves but has
	// no playable stream. It is this adapter's not-found vocabulary, the same
	// role adobotvhttp.ErrContentNotFound plays for the HTTP adapter.
	ErrContentNotFound = errors.New("file: content not found in this playlist")
)
