package file

import (
	"encoding/json"
	"sort"
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
// It derives a stable id for any item whose id is absent and drops later rows
// that duplicate an earlier id, so a lookup and a listing can never disagree
// about which item an id names.
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

	for i := range file.Channels {
		ch := file.Channels[i]
		if strings.TrimSpace(ch.ID) == "" {
			ch.ID = derivedChannelID(ch)
		}
		if _, dup := lib.channelsByID[ch.ID]; dup {
			continue
		}
		lib.channels = append(lib.channels, ch)
		lib.channelsByID[ch.ID] = ch
	}

	for i := range file.Entries {
		e := file.Entries[i]
		if strings.TrimSpace(e.ID) == "" {
			e.ID = derivedEntryID(e)
		}
		if _, dup := lib.entriesByID[e.ID]; dup {
			continue
		}
		lib.entries = append(lib.entries, e)
		lib.entriesByID[e.ID] = e
	}

	for i := range file.Episodes {
		ep := file.Episodes[i]
		if strings.TrimSpace(ep.ID) == "" {
			ep.ID = derivedEpisodeID(ep)
		}
		if _, dup := lib.episodesByID[ep.ID]; dup {
			continue
		}
		lib.episodesByID[ep.ID] = ep
		lib.episodesByVod[ep.VodID] = append(lib.episodesByVod[ep.VodID], ep)
	}

	for i := range file.Streams {
		s := file.Streams[i]
		if strings.TrimSpace(s.ID) == "" {
			s.ID = derivedStreamID(s)
		}
		lib.streamsByChannel[s.ChannelID] = append(lib.streamsByChannel[s.ChannelID], s)
	}

	for i := range file.VodStreams {
		s := file.VodStreams[i]
		if strings.TrimSpace(s.ID) == "" {
			s.ID = derivedVodStreamID(s)
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

// --- deterministic ordering ------------------------------------------------

// sortStreams orders live streams the way ResolveChannelStream prefers them:
// the default stream first, then active/online streams, then the rest, with a
// label-and-id tiebreak so the order never flaps between calls.
func sortStreams(streams []source.Stream) {
	sort.SliceStable(streams, func(i, j int) bool {
		a, b := streams[i], streams[j]
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		if ra, rb := statusRank(a.Status), statusRank(b.Status); ra != rb {
			return ra < rb
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.ID < b.ID
	})
}

// sortVodStreams is the same ordering for VOD streams ("playable default
// first"), which have no active/online distinction beyond status text.
func sortVodStreams(streams []source.VodStream) {
	sort.SliceStable(streams, func(i, j int) bool {
		a, b := streams[i], streams[j]
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		if ra, rb := statusRank(a.Status), statusRank(b.Status); ra != rb {
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

// sortChannels orders channels by category, then name, then id: the same
// display order postgres-direct's "ORDER BY category NULLS LAST, name" gives
// the client.
func sortChannels(channels []source.Channel) {
	sort.SliceStable(channels, func(i, j int) bool {
		a, b := channels[i], channels[j]
		if ca, cb := derefOr(a.Category, ""), derefOr(b.Category, ""); ca != cb {
			return ca < cb
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

// statusRank buckets a stream status for ordering: playable first, everything
// else after. "active" is what postgres-direct's ResolveChannelStream looks
// for; "online" is the status its GetVodStreams looks for. A file may use
// either, so both count as playable here.
func statusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "online":
		return 0
	default:
		return 1
	}
}
