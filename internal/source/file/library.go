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
// It normalises the filterable values once, here, so the value an adapter
// advertises and the value its filter accepts are always the same string (see
// normalizeOptional and distinctValues). It derives a stable id for any item
// whose id is absent, and drops a later row that duplicates an earlier
// *provided* id, so a lookup and a listing can never disagree about which item
// an id names.
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

	// usedStreamIDs tracks derived stream ids so a second stream whose
	// identity fields were omitted is kept under a suffixed id rather than
	// silently sharing the first's.
	usedStreamIDs := make(map[string]struct{}, len(file.Streams))
	usedVodStreamIDs := make(map[string]struct{}, len(file.VodStreams))

	for i := range file.Channels {
		ch := file.Channels[i]
		ch.Category = normalizeOptional(ch.Category)
		if strings.TrimSpace(ch.ID) == "" {
			ch.ID = uniqueDerivedID(derivedChannelID(ch), func(id string) bool {
				_, ok := lib.channelsByID[id]
				return ok
			})
		} else if _, dup := lib.channelsByID[ch.ID]; dup {
			continue
		}
		lib.channels = append(lib.channels, ch)
		lib.channelsByID[ch.ID] = ch
	}

	for i := range file.Entries {
		e := file.Entries[i]
		e.Category = normalizeOptional(e.Category)
		e.SourceType = strings.TrimSpace(e.SourceType)
		e.Type = strings.TrimSpace(e.Type)
		if strings.TrimSpace(e.ID) == "" {
			e.ID = uniqueDerivedID(derivedEntryID(e), func(id string) bool {
				_, ok := lib.entriesByID[id]
				return ok
			})
		} else if _, dup := lib.entriesByID[e.ID]; dup {
			continue
		}
		lib.entries = append(lib.entries, e)
		lib.entriesByID[e.ID] = e
	}

	for i := range file.Episodes {
		ep := file.Episodes[i]
		if strings.TrimSpace(ep.ID) == "" {
			ep.ID = uniqueDerivedID(derivedEpisodeID(ep), func(id string) bool {
				_, ok := lib.episodesByID[id]
				return ok
			})
		} else if _, dup := lib.episodesByID[ep.ID]; dup {
			continue
		}
		lib.episodesByID[ep.ID] = ep
		lib.episodesByVod[ep.VodID] = append(lib.episodesByVod[ep.VodID], ep)
	}

	for i := range file.Streams {
		s := file.Streams[i]
		if strings.TrimSpace(s.ID) == "" {
			s.ID = uniqueDerivedID(derivedStreamID(s), func(id string) bool {
				_, ok := usedStreamIDs[id]
				return ok
			})
			usedStreamIDs[s.ID] = struct{}{}
		}
		lib.streamsByChannel[s.ChannelID] = append(lib.streamsByChannel[s.ChannelID], s)
	}

	for i := range file.VodStreams {
		s := file.VodStreams[i]
		if strings.TrimSpace(s.ID) == "" {
			s.ID = uniqueDerivedID(derivedVodStreamID(s), func(id string) bool {
				_, ok := usedVodStreamIDs[id]
				return ok
			})
			usedVodStreamIDs[s.ID] = struct{}{}
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

// uniqueDerivedID returns base, or base with a numeric suffix when base is
// already taken. It keeps two genuinely distinct rows that happen to derive the
// same id — because the fields the derivation reads were omitted — instead of
// letting parseLibrary drop the second as a duplicate. The suffix follows file
// order, so it is deterministic across reloads of the same file.
func uniqueDerivedID(base string, used func(string) bool) string {
	if !used(base) {
		return base
	}
	for n := 2; ; n++ {
		if candidate := base + "-" + strconv.Itoa(n); !used(candidate) {
			return candidate
		}
	}
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
