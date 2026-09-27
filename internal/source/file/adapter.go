// Package file implements the file source adapter: a local playlist on disk,
// for a user with no AdoboTV account.
//
// It is the only adapter that reads nothing but a file. It opens no database,
// opens no network connection, and never writes to the file it reads. The
// playlist is loaded once in New into an in-memory library and every query
// answers from that value; source.Config.DB is ignored entirely, and this
// package imports no SQL package, so the read-only invariants in AGENTS.md hold
// structurally rather than by discipline.
//
// # The on-disk format
//
// The playlist is JSON that directly serialises the internal library types in
// internal/source/models.go. One top-level object with five arrays:
//
//	{
//	  "channels":    [ Channel ],
//	  "entries":     [ Entry ],
//	  "streams":     [ Stream ],
//	  "vod_streams": [ VodStream ],
//	  "episodes":    [ Episode ]
//	}
//
// Every array is optional. The field names are the models' own json tags, so
// Entry.SourceType is spelled "provider" and the rest are snake_case. See
// docs/source-adapters.md for a complete worked example.
//
// # Ids are opaque
//
// An item's id is whatever the file provides; the adapter never parses it.
// When a file omits one, the adapter derives a stable id from the item's
// identity fields (see ids.go and the docs). A child row references its parent
// by the id written in the file, so a file that wants its streams grouped must
// give the parent an id.
//
// # Capabilities
//
// It implements source.Source and the optional source.StreamProbeLister: a
// local playlist's streams can be enumerated, so AdoboFlix can tell its user
// which of their own streams are dead. It deliberately does NOT implement
// source.CompiledEPGProvider: a playlist file carries no compiled XMLTV blob,
// so the EPG endpoint reports source.UnsupportedEPGError rather than this
// adapter inventing an EPG feed it does not have.
package file

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// Name is the value of ADOBOFLIX_SOURCE that selects this adapter.
const Name = "file"

// The adapter must satisfy the read-only boundary and can enumerate the whole
// library's streams. It must NOT satisfy CompiledEPGProvider; that omission is
// what keeps the EPG capability honest and lets UnsupportedEPGError fire.
var (
	_ source.Source            = (*Adapter)(nil)
	_ source.StreamProbeLister = (*Adapter)(nil)
)

func init() {
	// The source.Config database handle is intentionally ignored: this
	// adapter has no database access by construction.
	source.Register(Name, func(source.Config) (source.Source, error) {
		return NewFromEnv()
	})
}

// Adapter is the file source. It holds the path it was loaded from and the
// parsed, immutable library every query reads. No mutex is needed: the library
// is never written after New returns.
type Adapter struct {
	path string
	lib  *library
}

// New reads a playlist file and parses it into an Adapter. A path that cannot
// be read returns ErrLibraryUnreadable; bytes that are not the documented JSON
// envelope return ErrMalformedLibrary. Both name the path, so a startup
// failure points at the file the user configured.
func New(path string) (*Adapter, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("file: a playlist path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrLibraryUnreadable, path, err)
	}
	lib, err := parseLibrary(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrMalformedLibrary, path, err)
	}
	return &Adapter{path: path, lib: lib}, nil
}

// Name returns the adapter's registered name.
func (a *Adapter) Name() string { return Name }

// GetStats returns aggregate VOD counts: the number of entries, the number of
// distinct providers, and the number of distinct genres (categories). It
// matches GetProviders and GetGenres, so the three always agree.
func (a *Adapter) GetStats() (*source.Stats, error) {
	providers, err := a.GetProviders()
	if err != nil {
		return nil, err
	}
	genres, err := a.GetGenres()
	if err != nil {
		return nil, err
	}
	return &source.Stats{
		TotalTitles:    len(a.lib.entries),
		TotalProviders: len(providers),
		TotalGenres:    len(genres),
	}, nil
}

// ListStreamsForProbe enumerates every live stream that carries a URL, with
// its channel metadata, so the scanner can report which of the user's own
// streams are dead. It implements the optional StreamProbeLister capability.
//
// Read-only and in memory: it never probes anything itself, and it never
// persists a result. Streams with no URL are skipped because there is nothing
// to probe. VOD streams and episodes are not enumerated: the report is about
// live channels, the same scope postgres-direct's probe listing uses.
func (a *Adapter) ListStreamsForProbe() ([]source.ProbeTarget, error) {
	channels := append([]source.Channel(nil), a.lib.channels...)
	sortChannels(channels)

	targets := []source.ProbeTarget{}
	for _, ch := range channels {
		for _, s := range a.lib.streamsByChannel[ch.ID] {
			if strings.TrimSpace(s.URL) == "" {
				continue
			}
			targets = append(targets, source.ProbeTarget{
				ChannelID:   ch.ID,
				ChannelName: ch.Name,
				Category:    derefOr(ch.Category, ""),
				StreamID:    s.ID,
				Label:       s.Label,
				URL:         s.URL,
				UserAgent:   s.UserAgent,
				Referer:     s.Referer,
			})
		}
	}
	return targets, nil
}

// derefOr returns the pointed-to string, or fallback when it is nil or empty.
func derefOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}
