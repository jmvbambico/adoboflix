package file

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// libraryFile is the on-disk JSON envelope: a direct serialisation of the
// internal models in internal/source/models.go, with no translation layer and
// no friendlier hand-authored format. One top-level object, five arrays. Every
// field is optional; an empty file is a valid empty library.
//
// The model json tags already present in models.go are what make this work,
// including Entry.SourceType's "provider" name and the snake_case names on the
// other types. Nothing in this package re-tags them.
type libraryFile struct {
	Channels   []source.Channel   `json:"channels"`
	Entries    []source.Entry     `json:"entries"`
	Streams    []source.Stream    `json:"streams"`
	VodStreams []source.VodStream `json:"vod_streams"`
	Episodes   []source.Episode   `json:"episodes"`
}

// library is the parsed, indexed playlist the query methods read. It is
// immutable once built and shared across goroutines without a lock.
//
// This is the seam that leaves room for a second parser: parseLibrary turns
// JSON bytes into a library, a future M3U parser would turn M3U bytes into the
// same value, and none of the query code below would change.
type library struct {
	entries  []source.Entry
	channels []source.Channel

	entriesByID  map[string]source.Entry
	channelsByID map[string]source.Channel
	episodesByID map[string]source.Episode

	streamsByChannel map[string][]source.Stream
	vodStreamsByVod  map[string][]source.VodStream
	episodesByVod    map[string][]source.Episode
}

// parseLibrary decodes the documented JSON envelope into an indexed library.
//
// Ids are assigned in two passes. The first reserves every id the file spells
// out; the second derives ids only for the rows that have none, drawing from
// outside that reserved set. So an explicit id always wins over a derived one
// wherever it sits in the file, and a derived id can never take an id the file
// meant for another row.
//
// Values are normalised once, here, so the value an adapter advertises and the
// value its filter accepts are always the same string (see normalizeOptional
// and distinctValues). Ids and the channel_id/vod_id references are trimmed for
// the same reason.
//
// A row whose own id is missing gets a derived one; a derived id that would
// collide with another derived id gets a deterministic numeric suffix, so a
// distinct row is never discarded. A row that repeats an id the file already
// gave an earlier row of the same kind is ambiguous, and is skipped for
// channels, entries and episodes. Streams and vod_streams are the exception:
// their rows are always kept, because two rows under one channel are separate
// streams and no interface method looks a stream up by id. Their derived ids
// still avoid every id the file provides.
func parseLibrary(data []byte) (*library, error) {
	var file libraryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	lib := &library{
		entriesByID:      make(map[string]source.Entry, len(file.Entries)),
		channelsByID:     make(map[string]source.Channel, len(file.Channels)),
		episodesByID:     make(map[string]source.Episode, len(file.Episodes)),
		streamsByChannel: make(map[string][]source.Stream, len(file.Channels)),
		vodStreamsByVod:  make(map[string][]source.VodStream, len(file.Entries)),
		episodesByVod:    make(map[string][]source.Episode, len(file.Entries)),
	}

	// Pass 1: every id the file spells out is reserved before any id is
	// derived, so a derived id can never collide with an explicit one however
	// the rows are ordered.
	channelIDs := collectIDs(file.Channels, func(c source.Channel) string { return c.ID })
	entryIDs := collectIDs(file.Entries, func(e source.Entry) string { return e.ID })
	episodeIDs := collectIDs(file.Episodes, func(e source.Episode) string { return e.ID })
	streamIDs := collectIDs(file.Streams, func(s source.Stream) string { return s.ID })
	vodStreamIDs := collectIDs(file.VodStreams, func(s source.VodStream) string { return s.ID })

	for i := range file.Channels {
		ch := file.Channels[i]
		ch.Category = normalizeOptional(ch.Category)
		if id := strings.TrimSpace(ch.ID); id != "" {
			ch.ID = id
			if _, dup := lib.channelsByID[id]; dup {
				continue
			}
		} else {
			ch.ID = channelIDs.claim(derivedChannelID(ch))
		}
		lib.channels = append(lib.channels, ch)
		lib.channelsByID[ch.ID] = ch
	}

	for i := range file.Entries {
		e := file.Entries[i]
		e.Category = normalizeOptional(e.Category)
		e.SourceType = strings.TrimSpace(e.SourceType)
		e.Type = strings.TrimSpace(e.Type)
		if id := strings.TrimSpace(e.ID); id != "" {
			e.ID = id
			if _, dup := lib.entriesByID[id]; dup {
				continue
			}
		} else {
			e.ID = entryIDs.claim(derivedEntryID(e))
		}
		lib.entries = append(lib.entries, e)
		lib.entriesByID[e.ID] = e
	}

	for i := range file.Episodes {
		ep := file.Episodes[i]
		ep.VodID = strings.TrimSpace(ep.VodID)
		if id := strings.TrimSpace(ep.ID); id != "" {
			ep.ID = id
			if _, dup := lib.episodesByID[id]; dup {
				continue
			}
		} else {
			ep.ID = episodeIDs.claim(derivedEpisodeID(ep))
		}
		lib.episodesByID[ep.ID] = ep
		lib.episodesByVod[ep.VodID] = append(lib.episodesByVod[ep.VodID], ep)
	}

	for i := range file.Streams {
		s := file.Streams[i]
		s.ChannelID = strings.TrimSpace(s.ChannelID)
		if id := strings.TrimSpace(s.ID); id != "" {
			s.ID = id
		} else {
			s.ID = streamIDs.claim(derivedStreamID(s))
		}
		lib.streamsByChannel[s.ChannelID] = append(lib.streamsByChannel[s.ChannelID], s)
	}

	for i := range file.VodStreams {
		s := file.VodStreams[i]
		s.VodID = strings.TrimSpace(s.VodID)
		if id := strings.TrimSpace(s.ID); id != "" {
			s.ID = id
		} else {
			s.ID = vodStreamIDs.claim(derivedVodStreamID(s))
		}
		lib.vodStreamsByVod[s.VodID] = append(lib.vodStreamsByVod[s.VodID], s)
	}

	// Go map iteration is random, so every child slice is sorted once here.
	// The query methods copy before re-sorting only where a caller's filter
	// changes the order; the common path reads these already-deterministic
	// slices.
	for id := range lib.streamsByChannel {
		sortStreams(lib.streamsByChannel[id])
	}
	for id := range lib.vodStreamsByVod {
		sortVodStreams(lib.vodStreamsByVod[id])
	}
	for id := range lib.episodesByVod {
		sortEpisodes(lib.episodesByVod[id])
	}

	return lib, nil
}

// --- normalisation ----------------------------------------------------------

// normalizeOptional trims an optional string and collapses a whitespace-only
// value to nil. It is applied once at parse time so the category an entry
// exposes is byte-identical to the category its filter compares against.
func normalizeOptional(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// distinctValues returns the unique, non-empty values, sorted. Equality is
// case-insensitive, so two values that filter identically (filterEntries uses
// EqualFold) are advertised once, under the casing of the first one seen.
// Case-sensitive DISTINCT is what postgres-direct gets from SQL, but there the
// filter is case-sensitive too; here the filter is not, so the advertisement
// must not be either.
func distinctValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// idSet is the set of ids one kind of row has claimed: every id the file
// spells out for that kind, plus every derived id already handed out. It is
// seeded with the file's own ids before any derivation, so a derived id can
// never take an explicit one.
type idSet map[string]struct{}

// collectIDs reserves every non-empty id the rows provide, trimmed.
func collectIDs[T any](rows []T, id func(T) string) idSet {
	set := make(idSet, len(rows))
	for _, row := range rows {
		if v := strings.TrimSpace(id(row)); v != "" {
			set[v] = struct{}{}
		}
	}
	return set
}

// claim returns base if it is free, otherwise base with the first free numeric
// suffix, and records the result. It keeps two genuinely distinct rows that
// derive the same id — because the fields the derivation reads were omitted —
// instead of dropping one as a duplicate. The suffix follows file order, so it
// is deterministic across reloads of the same file.
func (s idSet) claim(base string) string {
	if _, used := s[base]; !used {
		s[base] = struct{}{}
		return base
	}
	for n := 2; ; n++ {
		if candidate := base + "-" + strconv.Itoa(n); !s.has(candidate) {
			s[candidate] = struct{}{}
			return candidate
		}
	}
}

func (s idSet) has(id string) bool {
	_, ok := s[id]
	return ok
}

// --- deterministic ordering ------------------------------------------------

// sortStreams orders live streams the way ResolveChannelStream prefers them:
// the default stream first, then streams whose status is "active" (the status
// that method falls back on), then the rest, with a label-and-id tiebreak so
// the order never flaps between calls. VOD streams rank on "online" instead —
// see sortVodStreams — because that is the literal postgres-direct uses for
// each table.
func sortStreams(streams []source.Stream) {
	sort.SliceStable(streams, func(i, j int) bool {
		a, b := streams[i], streams[j]
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		if ra, rb := channelStatusRank(a.Status), channelStatusRank(b.Status); ra != rb {
			return ra < rb
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.ID < b.ID
	})
}

// sortVodStreams orders VOD streams "playable default first": the default
// stream first, then online streams, then the rest. The default-before-status
// precedence matches postgres-direct's GetVodStreams exactly
// (is_default DESC, (status = 'online') DESC, ...).
func sortVodStreams(streams []source.VodStream) {
	sort.SliceStable(streams, func(i, j int) bool {
		a, b := streams[i], streams[j]
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		if ra, rb := vodStatusRank(a.Status), vodStatusRank(b.Status); ra != rb {
			return ra < rb
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.ID < b.ID
	})
}

func sortEpisodes(episodes []source.Episode) {
	sort.SliceStable(episodes, func(i, j int) bool {
		a, b := episodes[i], episodes[j]
		if a.SeasonNumber != b.SeasonNumber {
			return a.SeasonNumber < b.SeasonNumber
		}
		if a.EpisodeNumber != b.EpisodeNumber {
			return a.EpisodeNumber < b.EpisodeNumber
		}
		return a.ID < b.ID
	})
}

// sortChannels orders channels by category, then name, then id — the same
// display order postgres-direct's "ORDER BY category NULLS LAST, name" gives
// the client. A channel with no category sorts after the categorised ones, not
// before them.
func sortChannels(channels []source.Channel) {
	sort.SliceStable(channels, func(i, j int) bool {
		a, b := channels[i], channels[j]
		switch {
		case a.Category != nil && b.Category != nil && *a.Category != *b.Category:
			return *a.Category < *b.Category
		case (a.Category == nil) != (b.Category == nil):
			return a.Category != nil
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

// sortEntriesNewestFirst orders by created_at descending, with rows that carry
// no timestamp after those that do (mirroring "NULLS LAST"), then by name and
// id so the order is stable when timestamps tie or are all absent.
func sortEntriesNewestFirst(entries []source.Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch {
		case a.CreatedAt != nil && b.CreatedAt != nil && !a.CreatedAt.Equal(*b.CreatedAt):
			return a.CreatedAt.After(*b.CreatedAt)
		case (a.CreatedAt == nil) != (b.CreatedAt == nil):
			return a.CreatedAt != nil
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

// channelStatusRank buckets a live-channel stream status for ordering: the
// status ResolveChannelStream's middle fallback looks for ("active") sorts
// first. Keeping the two in step is what lets GetChannelWithStreams and
// ResolveChannelStream agree on the stream they consider playable.
func channelStatusRank(status string) int {
	if strings.EqualFold(strings.TrimSpace(status), "active") {
		return 0
	}
	return 1
}

// vodStatusRank is channelStatusRank's VOD sibling. postgres-direct's
// GetVodStreams orders on status = 'online' (not 'active'), so VOD ranking
// deliberately differs from channel ranking.
func vodStatusRank(status string) int {
	if strings.EqualFold(strings.TrimSpace(status), "online") {
		return 0
	}
	return 1
}
