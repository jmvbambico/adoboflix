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
// The same adapter also loads an M3U/M3U8 playlist, selected by the file's
// extension. An M3U file has no types, ids or child rows, so the library is
// inferred rather than translated: entries in a group that reads as VOD become
// VOD assets (episodes grouped under one series, or a movie when no
// season/episode parses out of the title) and everything else becomes a live
// channel. That inference is guesswork, so the startup path logs a short
// summary of what it decided — see m3u.go and docs/source-adapters.md.
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
	"log"
	"os"
	"path/filepath"
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
	source.Register(Name, source.Requirement{}, func(source.Config) (source.Source, error) {
		return NewFromEnv()
	})
}

// Adapter is the file source. It holds the path it was loaded from, the parsed,
// immutable library every query reads, and — for an M3U playlist only — the
// summary of what the classifier inferred, which the startup path logs. No
// mutex is needed: none of it is written after New returns.
type Adapter struct {
	path    string
	lib     *library
	summary *m3uSummary
}

// New reads a playlist file and parses it into an Adapter.
//
// The parser is chosen by the file's extension — .json, or .m3u/.m3u8 — never
// by sniffing content, so a malformed playlist reports as malformed in the
// format its extension selected and is never silently re-parsed as the other.
// An extension this adapter does not know returns ErrUnsupportedFormat naming
// the supported set. A path that cannot be read returns ErrLibraryUnreadable;
// bytes that do not parse as the selected format return ErrMalformedLibrary.
// Every one names the path, so a startup failure points at the file the user
// configured.
func New(path string) (*Adapter, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("file: a playlist path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrLibraryUnreadable, path, err)
	}
	lib, summary, err := parseByExtension(path, data)
	if err != nil {
		return nil, err
	}
	return &Adapter{path: path, lib: lib, summary: summary}, nil
}

// parseByExtension dispatches to the parser the extension selects. The M3U
// branch also returns the classification summary; the JSON branch has no
// inference to describe, so it returns nil.
func parseByExtension(path string, data []byte) (*library, *m3uSummary, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case formatJSON:
		lib, err := parseLibrary(data)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %s: %v", ErrMalformedLibrary, path, err)
		}
		return lib, nil, nil
	case formatM3U, formatM3U8:
		lib, summary, err := parseM3U(data)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %s: %v", ErrMalformedLibrary, path, err)
		}
		return lib, summary, nil
	default:
		ext := filepath.Ext(path)
		if ext == "" {
			ext = "(none)"
		}
		return nil, nil, fmt.Errorf("%w: %s: extension %s is not one of %s",
			ErrUnsupportedFormat, path, ext, supportedFormats)
	}
}

// logSummary prints the M3U classification summary at startup, one line per
// category. It is a no-op for a JSON playlist, which carries no inference to
// report. A user who cannot find a title can read these lines to see whether it
// was filed as a channel or as VOD.
func (a *Adapter) logSummary() {
	if a.summary == nil {
		return
	}
	for _, line := range a.summary.logLines(a.path) {
		log.Print(line)
	}
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
