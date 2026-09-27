// Package postgresdirect implements the postgres-direct source adapter: a
// development test harness that reads the AdoboTV database directly.
//
// It exists to verify that the player resolves and plays content (DRM, Shaka,
// proxying) without standing up an AdoboTV account. It is not a supported
// end-user path: it bypasses entitlement, device authorisation and every
// analytics event AdoboTV records, and it must never become the default. The
// operator docs/source-adapters.md explains why at length; the short version
// is that AdoboTV records a playback event at /v1/drm/key/ and /v1/play/*, and
// a direct database tap makes every AdoboFlix user invisible to them.
//
// Every statement this adapter issues is a SELECT. It has no write method and
// never mutates AdoboTV state.
package postgresdirect

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// Name is the value of ADOBOFLIX_SOURCE that selects this adapter.
const Name = "postgres-direct"

// The adapter must satisfy the read-only boundary, and it can enumerate the
// whole library and read the compiled XMLTV blob, so it also offers the
// optional probe-listing and EPG capabilities.
var (
	_ source.Source              = (*Adapter)(nil)
	_ source.StreamProbeLister   = (*Adapter)(nil)
	_ source.CompiledEPGProvider = (*Adapter)(nil)
)

func init() {
	// postgres-direct is the only adapter that reads the schema directly, so it
	// is the only one that declares a database requirement. main opens the
	// connection for it and passes the handle in Config.DB.
	source.Register(Name, source.Requirement{Database: true}, func(cfg source.Config) (source.Source, error) {
		return New(cfg.DB)
	})
}

// Adapter reads the AdoboTV schema directly. It treats every id as the opaque
// uuid string the database stores; it never parses or synthesises one.
type Adapter struct {
	db *sqlx.DB
}

// New wraps a read-only SQL handle as a Source.
func New(db *sqlx.DB) (*Adapter, error) {
	if db == nil {
		return nil, errors.New("postgres-direct requires a database handle")
	}
	return &Adapter{db: db}, nil
}

// Name returns the adapter's registered name.
func (a *Adapter) Name() string { return Name }

// entryColumns lists the columns we explicitly select. Selecting "*" breaks
// because the table has columns we intentionally do not surface (e.g.
// check_status, last_check) and sqlx errors on unmapped destinations.
const entryColumns = `id, name, type, category, poster, background_image, plot,
	cast_members, directors, release_year, rating, status, stream_url,
	source_type, drm_type, drm_k, license_url, duration, user_agent, referer,
	tmdb_id, created_at, updated_at`

// channelColumns lists columns for channel queries.
const channelColumns = `id, name, logo, category, epg_source_id, epg_channel_id,
	status, created_at, updated_at`

// streamColumns lists columns for stream queries.
const streamColumns = `id, channel_id, label, url, source_type, drm_type, drm_k,
	license_url, is_default, status, user_agent, referer, resolution, bitrate, created_at`

// probeStreamColumns lists the columns a health probe needs, aliased to the
// ProbeTarget db tags. Explicit, never "*": the streams table carries columns
// this query does not map and sqlx errors on unmapped destinations.
const probeStreamColumns = `s.channel_id, s.id, COALESCE(s.label, '') AS label, s.url,
	s.user_agent, s.referer,
	c.name, COALESCE(c.category, 'Unknown') AS category`

// vodStreamColumns lists the columns we explicitly select. Selecting "*" breaks
// because the table carries columns we intentionally do not surface (e.g.
// last_check, check_status) and sqlx errors on unmapped destinations.
const vodStreamColumns = `id, vod_id, label, url, source_type, drm_type, drm_k,
	license_url, is_default, status, user_agent, referer, resolution, bitrate,
	created_at`

// episodeColumns lists columns for episode queries.
const episodeColumns = `id, vod_id, season_number, episode_number, name, stream_url,
	source_type, drm_type, drm_k, license_url, user_agent, referer`

func (a *Adapter) GetStats() (*source.Stats, error) {
	s := &source.Stats{}
	if err := a.db.QueryRow("SELECT COUNT(*) FROM vod_assets").Scan(&s.TotalTitles); err != nil {
		return nil, fmt.Errorf("count vod_assets: %w", err)
	}
	// Best effort, as before: a failure leaves the count at zero rather than
	// failing the whole page.
	a.db.QueryRow("SELECT COUNT(DISTINCT source_type) FROM vod_assets WHERE source_type IS NOT NULL").Scan(&s.TotalProviders)
	a.db.QueryRow("SELECT COUNT(DISTINCT category) FROM vod_assets WHERE category IS NOT NULL AND category != ''").Scan(&s.TotalGenres)
	return s, nil
}

func (a *Adapter) GetEntries(provider, genre, contentType string, page, limit int) ([]source.Entry, int, error) {
	offset := (page - 1) * limit
	args := []interface{}{}
	where := "WHERE 1=1"

	if provider != "" {
		where += " AND source_type = $" + strconv.Itoa(len(args)+1)
		args = append(args, provider)
	}
	if genre != "" {
		where += " AND category = $" + strconv.Itoa(len(args)+1)
		args = append(args, genre)
	}
	if contentType != "" {
		where += " AND LOWER(type::text) = LOWER($" + strconv.Itoa(len(args)+1) + ")"
		args = append(args, contentType)
	}

	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM vod_assets "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count vod_assets: %w", err)
	}

	entries := []source.Entry{}
	query := "SELECT " + entryColumns + " FROM vod_assets " + where +
		" ORDER BY created_at DESC NULLS LAST LIMIT $" + strconv.Itoa(len(args)+1) +
		" OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	if err := a.db.Select(&entries, query, args...); err != nil {
		return nil, 0, fmt.Errorf("list vod_assets: %w", err)
	}
	return entries, total, nil
}

func (a *Adapter) GetEntry(id string) (*source.Entry, error) {
	var e source.Entry
	if err := a.db.Get(&e, "SELECT "+entryColumns+" FROM vod_assets WHERE id = $1", id); err != nil {
		return nil, fmt.Errorf("get vod asset %s: %w", id, err)
	}
	return &e, nil
}

func (a *Adapter) Search(q, provider, genre, contentType string, page, limit int) ([]source.Entry, int, error) {
	offset := (page - 1) * limit
	args := []interface{}{"%" + q + "%"}
	where := "WHERE name ILIKE $1"

	if provider != "" {
		where += " AND source_type = $" + strconv.Itoa(len(args)+1)
		args = append(args, provider)
	}
	if genre != "" {
		where += " AND category = $" + strconv.Itoa(len(args)+1)
		args = append(args, genre)
	}
	if contentType != "" {
		where += " AND LOWER(type::text) = LOWER($" + strconv.Itoa(len(args)+1) + ")"
		args = append(args, contentType)
	}

	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM vod_assets "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count search matches: %w", err)
	}

	entries := []source.Entry{}
	query := "SELECT " + entryColumns + " FROM vod_assets " + where +
		" ORDER BY created_at DESC NULLS LAST LIMIT $" + strconv.Itoa(len(args)+1) +
		" OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	if err := a.db.Select(&entries, query, args...); err != nil {
		return nil, 0, fmt.Errorf("search vod_assets: %w", err)
	}
	return entries, total, nil
}

func (a *Adapter) GetProviders() ([]string, error) {
	var p []string
	if err := a.db.Select(&p, "SELECT DISTINCT source_type::text FROM vod_assets WHERE source_type IS NOT NULL ORDER BY source_type"); err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	return p, nil
}

func (a *Adapter) GetGenres() ([]string, error) {
	var g []string
	if err := a.db.Select(&g, "SELECT DISTINCT category FROM vod_assets WHERE category IS NOT NULL AND category != '' ORDER BY category"); err != nil {
		return nil, fmt.Errorf("list genres: %w", err)
	}
	return g, nil
}

// EpisodeCounts resolves every id in one aggregate query rather than one call
// per asset.
func (a *Adapter) EpisodeCounts(vodIDs []string) (map[string]int, error) {
	counts := map[string]int{}
	if len(vodIDs) == 0 {
		return counts, nil
	}

	type countRow struct {
		VodID string `db:"vod_id"`
		Count int    `db:"episode_count"`
	}
	var rows []countRow
	query := `SELECT vod_id, COUNT(*) AS episode_count FROM episodes WHERE vod_id = ANY($1) GROUP BY vod_id`
	if err := a.db.Select(&rows, query, pq.Array(vodIDs)); err != nil {
		return nil, fmt.Errorf("count episodes for %d vod ids: %w", len(vodIDs), err)
	}
	for _, r := range rows {
		counts[r.VodID] = r.Count
	}
	return counts, nil
}

// GetEpisodes returns all episodes for a given VOD asset, sorted by
// season then episode.
func (a *Adapter) GetEpisodes(vodID string) ([]source.Episode, error) {
	episodes := []source.Episode{}
	query := "SELECT " + episodeColumns + " FROM episodes WHERE vod_id = $1 ORDER BY season_number, episode_number"
	if err := a.db.Select(&episodes, query, vodID); err != nil {
		return nil, fmt.Errorf("list episodes for vod %s: %w", vodID, err)
	}
	return episodes, nil
}

// GetEpisode returns a single episode by ID.
func (a *Adapter) GetEpisode(episodeID string) (*source.Episode, error) {
	var e source.Episode
	query := "SELECT " + episodeColumns + " FROM episodes WHERE id = $1"
	if err := a.db.Get(&e, query, episodeID); err != nil {
		return nil, fmt.Errorf("get episode %s: %w", episodeID, err)
	}
	return &e, nil
}

// GetVodStreams returns every stream row for a VOD asset, ordered so the
// playable default comes first: is_default DESC (unique partial index
// guarantees at most one default), then online before any other status,
// then a stable tiebreak of bitrate DESC NULLS LAST and label.
func (a *Adapter) GetVodStreams(vodID string) ([]source.VodStream, error) {
	streams := []source.VodStream{}
	query := "SELECT " + vodStreamColumns + ` FROM vod_streams WHERE vod_id = $1
		ORDER BY is_default DESC,
			(status = 'online') DESC,
			bitrate DESC NULLS LAST,
			label`
	if err := a.db.Select(&streams, query, vodID); err != nil {
		return nil, fmt.Errorf("get vod_streams for vod %s: %w", vodID, err)
	}
	return streams, nil
}

// ListChannels returns channels, optionally filtered by category.
func (a *Adapter) ListChannels(category string, limit, offset int) ([]source.Channel, int, error) {
	args := []interface{}{}
	where := "WHERE 1=1"
	if category != "" && category != "All" {
		where += " AND category = $" + strconv.Itoa(len(args)+1)
		args = append(args, category)
	}

	var total int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM channels "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count channels: %w", err)
	}

	channels := []source.Channel{}
	query := "SELECT " + channelColumns + " FROM channels " + where +
		" ORDER BY category NULLS LAST, name LIMIT $" + strconv.Itoa(len(args)+1) +
		" OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	if err := a.db.Select(&channels, query, args...); err != nil {
		return nil, 0, fmt.Errorf("list channels: %w", err)
	}
	return channels, total, nil
}

// GetChannel returns a single channel by ID.
func (a *Adapter) GetChannel(channelID string) (*source.Channel, error) {
	var c source.Channel
	if err := a.db.Get(&c, "SELECT "+channelColumns+" FROM channels WHERE id = $1", channelID); err != nil {
		return nil, fmt.Errorf("get channel %s: %w", channelID, err)
	}
	return &c, nil
}

// GetChannelWithStreams returns a channel and all its streams ordered by
// is_default DESC.
func (a *Adapter) GetChannelWithStreams(channelID string) (*source.Channel, []source.Stream, error) {
	c, err := a.GetChannel(channelID)
	if err != nil {
		return nil, nil, err
	}

	streams := []source.Stream{}
	query := "SELECT " + streamColumns + " FROM streams WHERE channel_id = $1 ORDER BY is_default DESC, bitrate DESC NULLS LAST"
	if err := a.db.Select(&streams, query, channelID); err != nil {
		return nil, nil, fmt.Errorf("list streams for channel %s: %w", channelID, err)
	}
	return c, streams, nil
}

// ListChannelCategories returns distinct channel categories.
func (a *Adapter) ListChannelCategories() ([]string, error) {
	var cats []string
	if err := a.db.Select(&cats, `SELECT DISTINCT category FROM channels WHERE category IS NOT NULL AND category != '' ORDER BY category`); err != nil {
		return nil, fmt.Errorf("list channel categories: %w", err)
	}
	return cats, nil
}

// ResolveChannelStream returns the default stream for a channel, with fallback
// to the first active stream and then to any stream at all.
func (a *Adapter) ResolveChannelStream(channelID string) (*source.Stream, error) {
	var s source.Stream
	// Try default stream first
	query := "SELECT " + streamColumns + " FROM streams WHERE channel_id = $1 AND is_default = true LIMIT 1"
	if err := a.db.Get(&s, query, channelID); err == nil {
		return &s, nil
	}
	// Fallback: first active stream
	query = "SELECT " + streamColumns + " FROM streams WHERE channel_id = $1 AND status = 'active' LIMIT 1"
	if err := a.db.Get(&s, query, channelID); err == nil {
		return &s, nil
	}
	// Fallback: any stream at all
	query = "SELECT " + streamColumns + " FROM streams WHERE channel_id = $1 ORDER BY is_default DESC LIMIT 1"
	if err := a.db.Get(&s, query, channelID); err != nil {
		return nil, fmt.Errorf("resolve stream for channel %s: %w", channelID, err)
	}
	return &s, nil
}

// ListStreamsForProbe enumerates every probeable stream in the library with
// its channel metadata. It implements the optional StreamProbeLister
// capability: health scanning needs the whole library, which only a
// database-backed adapter can see.
//
// Read-only: a single SELECT joining streams to channels. It selects the
// probe's columns explicitly — never "*" — and skips rows with no usable URL.
func (a *Adapter) ListStreamsForProbe() ([]source.ProbeTarget, error) {
	targets := []source.ProbeTarget{}
	query := `SELECT ` + probeStreamColumns + `
		FROM streams s
		JOIN channels c ON c.id = s.channel_id
		WHERE s.url IS NOT NULL AND s.url != ''
		ORDER BY c.name, s.is_default DESC`
	if err := a.db.Select(&targets, query); err != nil {
		return nil, fmt.Errorf("list streams for probe: %w", err)
	}
	return targets, nil
}

// CompiledEPG returns the newest compiled XMLTV blob, still gzipped, with the
// hash the compiler recorded for it. It implements the optional
// CompiledEPGProvider capability: an HTTP adapter fetches the same gzipped
// bytes from the playlist envelope's pre-tokenized EPG URL, and internal/epg
// owns decompressing and parsing them for both.
//
// Read-only: a single SELECT of the two columns the EPG service needs, newest
// row first. Columns are explicit — never "*".
func (a *Adapter) CompiledEPG() ([]byte, string, error) {
	type compiledRow struct {
		Data []byte `db:"data"`
		Hash string `db:"hash"`
	}
	var row compiledRow
	if err := a.db.Get(&row, "SELECT data, hash FROM compiled_epg ORDER BY updated_at DESC LIMIT 1"); err != nil {
		return nil, "", fmt.Errorf("read compiled_epg: %w", err)
	}
	return row.Data, row.Hash, nil
}
