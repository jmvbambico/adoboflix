package source

import "fmt"

// CompiledEPGProvider is an OPTIONAL capability. A Source implements it when
// it can supply the compiled XMLTV blob. postgres-direct reads it from the
// library table; an HTTP adapter fetches it from the playlist envelope's
// pre-tokenized EPG URL. Both return the same thing: gzipped XMLTV bytes.
//
// The blob is returned still gzipped, with the hash the compiler recorded for
// it. Decompressing and parsing XMLTV is identical for every source, so it
// stays in internal/epg; moving it into the adapters would only duplicate it.
//
// Callers must type-assert for this capability and report a clear unsupported
// error when it is absent. It is deliberately not part of Source, so that
// every adapter is not forced to fake an EPG feed it cannot supply.
type CompiledEPGProvider interface {
	// CompiledEPG returns the current compiled XMLTV blob, still gzipped,
	// plus the hash the compiler recorded for it. It is read-only.
	CompiledEPG() (data []byte, hash string, err error)
}

// UnsupportedEPGError is the honest error a caller reports when the active
// source does not implement CompiledEPGProvider. It names the source and says
// plainly that EPG is unsupported for it, so the user sees why the EPG
// endpoint is unavailable instead of a panic or a silently empty response.
func UnsupportedEPGError(name string) error {
	return fmt.Errorf("source %q does not provide EPG data: it has no compiled XMLTV blob to supply", name)
}
