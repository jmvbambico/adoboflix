// Package source defines AdoboFlix's read-only content boundary.
//
// Content reaches AdoboFlix through a source adapter. Every adapter implements
// Source and every method on it is a read: there is no write method, and an
// adapter must never persist anything upstream. That is the First Law in
// AGENTS.md and it applies to adapters without exception.
//
// # Ids are opaque
//
// Adapters do not agree on what a content id is. postgres-direct hands out
// channels.id / vod_assets.id uuids; adobotv-http has no id in its playlist
// envelope at all (the real id is sealed inside an encrypted blob) and
// identifies channels by name + epg_id; a file adapter uses whatever its
// playlist provides. So the ids crossing this interface are opaque strings
// owned by the adapter that produced them: never parse them, never assume a
// uuid shape, and never round-trip them through anything that does.
//
// Callers treat an id as a token to hand back: handler code reads an id out of
// a response and passes it straight back into the next adapter call.
package source

// Source is the read-only content boundary between AdoboFlix and whatever
// library it is pointed at. Each adapter — adobotv-http, file, postgres-direct
// — implements it, and handlers depend on this interface and nothing below it.
//
// The method set is derived from the real call sites in internal/handler.
// Every id parameter and every ID field it returns is an opaque string as
// described in the package doc.
type Source interface {
	// Name returns the adapter's registered name, e.g. "postgres-direct".
	// Callers use it to name the active source in errors and logs.
	Name() string

	// GetStats returns aggregate library counts.
	GetStats() (*Stats, error)

	// GetEntries lists VOD assets, optionally filtered, newest first. It
	// returns the page's entries and the total number matching the filter.
	GetEntries(provider, genre, contentType string, page, limit int) ([]Entry, int, error)

	// GetEntry returns a single VOD asset by its opaque id.
	GetEntry(id string) (*Entry, error)

	// Search lists VOD assets whose name matches q, with the same filters as
	// GetEntries. It returns the page's entries and the total match count.
	Search(q, provider, genre, contentType string, page, limit int) ([]Entry, int, error)

	// GetProviders returns the distinct source_type values in the library.
	GetProviders() ([]string, error)

	// GetGenres returns the distinct category values in the library.
	GetGenres() ([]string, error)

	// EpisodeCounts returns, for each of the given opaque VOD ids, how many
	// episodes it has. Ids with no episodes are absent from the map.
	EpisodeCounts(vodIDs []string) (map[string]int, error)

	// GetVodStreams returns every stream row for a VOD asset, ordered so the
	// playable default comes first.
	GetVodStreams(vodID string) ([]VodStream, error)

	// GetEpisodes returns every episode of a VOD asset, ordered by
	// season then episode.
	GetEpisodes(vodID string) ([]Episode, error)

	// GetEpisode returns a single episode by its opaque id.
	GetEpisode(episodeID string) (*Episode, error)

	// ListChannels returns channels for a page, optionally filtered by
	// category, plus the total number matching the filter.
	ListChannels(category string, limit, offset int) ([]Channel, int, error)

	// ListChannelCategories returns the distinct channel categories.
	ListChannelCategories() ([]string, error)

	// GetChannel returns a single channel by its opaque id.
	GetChannel(channelID string) (*Channel, error)

	// GetChannelWithStreams returns a channel and all of its streams.
	GetChannelWithStreams(channelID string) (*Channel, []Stream, error)

	// ResolveChannelStream returns the stream a channel should play: the
	// default one, else the first active, else any.
	ResolveChannelStream(channelID string) (*Stream, error)
}
