package file

import (
	"fmt"
	"strings"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// --- VOD (Source) -----------------------------------------------------------

// GetEntries lists VOD assets, filtered by provider, genre and content type,
// newest first, and returns the page plus the total matching count. Filtering,
// ordering and paging all happen over the in-memory library.
func (a *Adapter) GetEntries(provider, genre, contentType string, page, limit int) ([]source.Entry, int, error) {
	filtered := filterEntries(a.lib.entries, provider, genre, contentType)
	sortEntriesNewestFirst(filtered)
	pageEntries, total := paginate(filtered, page, limit)
	return pageEntries, total, nil
}

// GetEntry returns a single VOD asset by its opaque id.
func (a *Adapter) GetEntry(id string) (*source.Entry, error) {
	e, ok := a.lib.entriesByID[id]
	if !ok {
		return nil, fmt.Errorf("%w: no entry with id %q", ErrContentNotFound, id)
	}
	return &e, nil
}

// Search lists VOD assets whose name matches q, with the same provider, genre
// and content-type filters as GetEntries and the same paging. An empty q
// matches everything, so Search with no term is a filter-only listing.
func (a *Adapter) Search(q, provider, genre, contentType string, page, limit int) ([]source.Entry, int, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	matched := make([]source.Entry, 0, len(a.lib.entries))
	for _, e := range a.lib.entries {
		if needle != "" && !strings.Contains(strings.ToLower(e.Name), needle) {
			continue
		}
		matched = append(matched, e)
	}
	filtered := filterEntries(matched, provider, genre, contentType)
	sortEntriesNewestFirst(filtered)
	pageEntries, total := paginate(filtered, page, limit)
	return pageEntries, total, nil
}

// GetProviders returns the distinct provider (source_type) values in the
// library, sorted. An empty library yields an empty slice, not nil. Values are
// deduplicated case-insensitively, so the list never advertises two providers
// that filterEntries treats as one.
func (a *Adapter) GetProviders() ([]string, error) {
	providers := make([]string, 0, len(a.lib.entries))
	for _, e := range a.lib.entries {
		providers = append(providers, e.SourceType)
	}
	return distinctValues(providers), nil
}

// GetGenres returns the distinct non-empty categories in the library, sorted.
// An empty library yields an empty slice, not nil. Values are deduplicated
// case-insensitively, so the list never advertises two genres that
// filterEntries treats as one.
func (a *Adapter) GetGenres() ([]string, error) {
	genres := make([]string, 0, len(a.lib.entries))
	for _, e := range a.lib.entries {
		genres = append(genres, derefOr(e.Category, ""))
	}
	return distinctValues(genres), nil
}

// EpisodeCounts returns, for each requested VOD id, how many episodes it has.
// An id with no episodes — and an id that is not in the library at all — is
// absent from the map rather than present with a zero, matching the interface
// contract and the sibling adapters.
func (a *Adapter) EpisodeCounts(vodIDs []string) (map[string]int, error) {
	counts := make(map[string]int, len(vodIDs))
	for _, id := range vodIDs {
		if n := len(a.lib.episodesByVod[id]); n > 0 {
			counts[id] = n
		}
	}
	return counts, nil
}

// GetVodStreams returns every stream row for a VOD asset, ordered so the
// playable default comes first. An unknown vod id is a not-found error; a known
// asset with no stream rows is an empty list, which is the honest answer for a
// series whose playable leaves are its episodes.
func (a *Adapter) GetVodStreams(vodID string) ([]source.VodStream, error) {
	if _, ok := a.lib.entriesByID[vodID]; !ok {
		return nil, fmt.Errorf("%w: no VOD entry with id %q", ErrContentNotFound, vodID)
	}
	src := a.lib.vodStreamsByVod[vodID]
	streams := make([]source.VodStream, len(src))
	copy(streams, src)
	sortVodStreams(streams)
	return streams, nil
}

// GetEpisodes returns every episode of a VOD asset, ordered by season then
// episode. An unknown vod id is a not-found error; a known asset with no
// episodes is an empty list.
func (a *Adapter) GetEpisodes(vodID string) ([]source.Episode, error) {
	if _, ok := a.lib.entriesByID[vodID]; !ok {
		return nil, fmt.Errorf("%w: no VOD entry with id %q", ErrContentNotFound, vodID)
	}
	src := a.lib.episodesByVod[vodID]
	episodes := make([]source.Episode, len(src))
	copy(episodes, src)
	sortEpisodes(episodes)
	return episodes, nil
}

// GetEpisode returns a single episode by its opaque id.
func (a *Adapter) GetEpisode(episodeID string) (*source.Episode, error) {
	ep, ok := a.lib.episodesByID[episodeID]
	if !ok {
		return nil, fmt.Errorf("%w: no episode with id %q", ErrContentNotFound, episodeID)
	}
	return &ep, nil
}

// --- channels (Source) ------------------------------------------------------

// ListChannels returns channels for a page, optionally filtered by category,
// plus the total matching the filter. "" and "All" both mean no filter.
func (a *Adapter) ListChannels(category string, limit, offset int) ([]source.Channel, int, error) {
	filtered := make([]source.Channel, 0, len(a.lib.channels))
	for _, ch := range a.lib.channels {
		if !channelMatchesCategory(ch, category) {
			continue
		}
		filtered = append(filtered, ch)
	}
	sortChannels(filtered)
	return offsetPaginate(filtered, limit, offset), len(filtered), nil
}

// ListChannelCategories returns the distinct non-empty channel categories,
// sorted. Values are deduplicated case-insensitively, so the list never
// advertises two categories that channelMatchesCategory treats as one.
func (a *Adapter) ListChannelCategories() ([]string, error) {
	categories := make([]string, 0, len(a.lib.channels))
	for _, ch := range a.lib.channels {
		categories = append(categories, derefOr(ch.Category, ""))
	}
	return distinctValues(categories), nil
}

// GetChannel returns a single channel by its opaque id.
func (a *Adapter) GetChannel(channelID string) (*source.Channel, error) {
	ch, ok := a.lib.channelsByID[channelID]
	if !ok {
		return nil, fmt.Errorf("%w: no channel with id %q", ErrContentNotFound, channelID)
	}
	return &ch, nil
}

// GetChannelWithStreams returns a channel and all of its streams, ordered so
// the playable default comes first. An unknown channel is a not-found error; a
// channel with no streams returns an empty list.
func (a *Adapter) GetChannelWithStreams(channelID string) (*source.Channel, []source.Stream, error) {
	ch, ok := a.lib.channelsByID[channelID]
	if !ok {
		return nil, nil, fmt.Errorf("%w: no channel with id %q", ErrContentNotFound, channelID)
	}
	src := a.lib.streamsByChannel[channelID]
	streams := make([]source.Stream, len(src))
	copy(streams, src)
	sortStreams(streams)
	return &ch, streams, nil
}

// ResolveChannelStream returns the stream a channel should play, using the
// same precedence postgres-direct uses: the stream flagged default, else the
// first active stream, else any stream at all. A channel with no streams at
// all is a not-found error, because there is nothing to play.
func (a *Adapter) ResolveChannelStream(channelID string) (*source.Stream, error) {
	if _, ok := a.lib.channelsByID[channelID]; !ok {
		return nil, fmt.Errorf("%w: no channel with id %q", ErrContentNotFound, channelID)
	}
	streams := make([]source.Stream, len(a.lib.streamsByChannel[channelID]))
	copy(streams, a.lib.streamsByChannel[channelID])
	sortStreams(streams)

	for i := range streams {
		if streams[i].IsDefault {
			return &streams[i], nil
		}
	}
	for i := range streams {
		if strings.EqualFold(strings.TrimSpace(streams[i].Status), "active") {
			return &streams[i], nil
		}
	}
	if len(streams) > 0 {
		return &streams[0], nil
	}
	return nil, fmt.Errorf("%w: channel %q has no playable stream", ErrContentNotFound, channelID)
}

// --- helpers ----------------------------------------------------------------

// filterEntries applies the provider / genre / contentType filters the
// interface promises. Matching is case-insensitive, following the other
// non-SQL adapter rather than the SQL one's exact match. Genre maps to Entry's
// category: the internal Entry type carries no separate genre field.
func filterEntries(entries []source.Entry, provider, genre, contentType string) []source.Entry {
	out := make([]source.Entry, 0, len(entries))
	for _, e := range entries {
		if provider != "" && !strings.EqualFold(provider, e.SourceType) {
			continue
		}
		if genre != "" && !strings.EqualFold(genre, derefOr(e.Category, "")) {
			continue
		}
		if contentType != "" && !strings.EqualFold(contentType, e.Type) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func channelMatchesCategory(ch source.Channel, category string) bool {
	if category == "" || strings.EqualFold(category, "All") {
		return true
	}
	return strings.EqualFold(derefOr(ch.Category, ""), category)
}

// paginate returns the page slice and the total for a 1-based page number,
// mirroring the SQL adapter's LIMIT/OFFSET arithmetic. A non-positive limit
// means "no limit" on the first page and an empty result beyond it, the same
// convention adobotv-http uses.
func paginate[T any](items []T, page, limit int) ([]T, int) {
	total := len(items)
	if limit <= 0 {
		if page <= 1 {
			return items, total
		}
		return []T{}, total
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * limit
	if start >= total {
		return []T{}, total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return items[start:end], total
}

// offsetPaginate is paginate's offset-based sibling, used by ListChannels.
// A non-positive limit means "everything from offset".
func offsetPaginate[T any](items []T, limit, offset int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []T{}
	}
	if limit <= 0 {
		return items[offset:]
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
