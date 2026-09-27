package source

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrSyncUnsupported is returned when a sync is asked of a source that cannot
// perform one. It is not a fault: a source with nothing to refresh — a local
// playlist read once from disk — simply has no SyncProvider capability, and the
// caller reports that plainly rather than pretending the sync succeeded.
var ErrSyncUnsupported = errors.New("source does not support sync")

// UnsupportedSyncError builds the user-facing error for a sync request against a
// source that cannot sync, naming the adapter so the message points at the
// actual source rather than a generic failure.
func UnsupportedSyncError(name string) error {
	if name == "" {
		name = "none"
	}
	return fmt.Errorf("%w: source %q has nothing to refresh; a locally imported playlist is a snapshot you re-import", ErrSyncUnsupported, name)
}

// SyncProvider is an OPTIONAL capability. A Source implements it when it holds
// an upstream-backed library that can be refreshed ahead of use: it can
// re-fetch its content now, and it can report when it last did so.
//
// It exists because only the HTTP subscriber path has remote content to sync.
// A local playlist adapter parses its file once when it opens and has nothing
// to re-read, and it deliberately does not implement this — so callers show
// sync state and offer "sync now" only where it is real, by type-asserting for
// this capability rather than matching adapter names.
//
// Refresh is read-only: it re-reads the library through the same read path a
// normal request uses and persists nothing upstream. A failed refresh must not
// disturb the cache the caller is already serving from — the next normal read
// remains the source of truth — so a partial failure leaves the previous
// library in place and is reported as an error the caller logs and ignores.
type SyncProvider interface {
	// Refresh re-fetches the source's library now, so the next read is warm.
	// It is read-only and best-effort: on failure it returns the error and
	// leaves whatever was already cached untouched.
	Refresh(ctx context.Context) error

	// LastSyncedAt reports when the library was last fetched from upstream,
	// from facts the adapter already holds. ok is false when nothing has been
	// fetched yet, which callers report by omitting a timestamp rather than
	// sending a zero time. It reads local state only and never touches the
	// network.
	LastSyncedAt() (time.Time, bool)
}
