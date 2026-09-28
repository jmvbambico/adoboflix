package source

import "context"

// ContextChannelLister is an OPTIONAL capability. A Source implements it when
// its channel listing can be bounded by a caller's context — in practice, when
// it reaches the network and can therefore honour a cancellation.
//
// It exists for the playlist-code validation, which is the one place AdoboFlix
// makes a real upstream read outside a normal request: the boot re-check of an
// aged stored credential. Without this, that read would ignore the caller's
// context and a shutdown would wait for it to time out (the adapter's HTTP
// timeout, DefaultTimeout). With it, cancelling the context aborts the request
// and shutdown returns promptly.
//
// It is deliberately separate from Source.ListChannels, which stays
// context-free: changing that signature would touch every adapter and handler
// for the sake of one caller. Callers that hold a context and a Source
// type-assert for this capability and fall back to ListChannels when it is
// absent, so a non-network adapter (a local file, a test stub) is unaffected.
//
// # A network-backed adapter should implement this
//
// Anything that reaches the network should implement ContextChannelLister.
// Nothing enforces it — adoption is per-adapter and there is no lint or test
// that catches an omission — and what is lost by skipping it is concrete: the
// call falls back to Source.ListChannels with context.Background(), so a boot
// credential re-check against an unreachable upstream cannot be cancelled and a
// shutdown waits out that adapter's HTTP timeout (adobotv-http's DefaultTimeout,
// 30s) before the process can exit. An adapter that reads only local state has
// nothing to cancel and correctly does not implement this.
type ContextChannelLister interface {
	// ListChannelsContext is ListChannels with the caller's context, so the
	// underlying read is cancelled when the context is. It is read-only.
	ListChannelsContext(ctx context.Context, category string, limit, offset int) ([]Channel, int, error)
}
