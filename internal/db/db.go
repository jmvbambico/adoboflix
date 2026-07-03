package db

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
)

type DB struct {
	*sqlx.DB
}

// Entry maps the public.vod_assets table from the AdoboTV PostgreSQL database.
type Entry struct {
	ID              string         `db:"id" json:"id"`
	Name            string         `db:"name" json:"name"`
	Type            string         `db:"type" json:"type"`
	Category        *string        `db:"category" json:"category,omitempty"`
	Poster          *string        `db:"poster" json:"poster,omitempty"`
	BackgroundImage *string        `db:"background_image" json:"background_image,omitempty"`
	Plot            *string        `db:"plot" json:"plot,omitempty"`
	CastMembers     pq.StringArray `db:"cast_members" json:"cast_members"`
	Directors       pq.StringArray `db:"directors" json:"directors"`
	ReleaseYear     *int           `db:"release_year" json:"release_year,omitempty"`
	Rating          *string        `db:"rating" json:"rating,omitempty"`
	Status          string         `db:"status" json:"status"`
	StreamURL       *string        `db:"stream_url" json:"stream_url,omitempty"`
	SourceType      string         `db:"source_type" json:"provider"`
	DrmType         *string        `db:"drm_type" json:"drm_type,omitempty"`
	DrmK            *string        `db:"drm_k" json:"drm_k,omitempty"`
	LicenseURL      *string        `db:"license_url" json:"license_url,omitempty"`
	Duration        *int           `db:"duration" json:"duration,omitempty"`
	UserAgent       *string        `db:"user_agent" json:"user_agent,omitempty"`
	Referer         *string        `db:"referer" json:"referer,omitempty"`
	TmdbID          *int           `db:"tmdb_id" json:"tmdb_id,omitempty"`
	CreatedAt       *time.Time     `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt       *time.Time     `db:"updated_at" json:"updated_at,omitempty"`
}

// Episode maps the public.episodes table from the AdoboTV PostgreSQL database.
type Episode struct {
	ID            string  `db:"id" json:"id"`
	VodID         string  `db:"vod_id" json:"vod_id"`
	SeasonNumber  int     `db:"season_number" json:"season_number"`
	EpisodeNumber int     `db:"episode_number" json:"episode_number"`
	Name          string  `db:"name" json:"name"`
	StreamURL     *string `db:"stream_url" json:"stream_url"`
	SourceType    string  `db:"source_type" json:"source_type"`
	DrmType       *string `db:"drm_type" json:"drm_type"`
	DrmK          *string `db:"drm_k" json:"drm_k"`
	LicenseURL    *string `db:"license_url" json:"license_url"`
	UserAgent     *string `db:"user_agent" json:"user_agent"`
	Referer       *string `db:"referer" json:"referer"`
}

// entryColumns lists the columns we explicitly select. Selecting "*" breaks
// because the table has columns we intentionally do not surface (e.g.
// check_status, last_check) and sqlx errors on unmapped destinations.
const entryColumns = `id, name, type, category, poster, background_image, plot,
	cast_members, directors, release_year, rating, status, stream_url,
	source_type, drm_type, drm_k, license_url, duration, user_agent, referer,
	tmdb_id, created_at, updated_at`

// --- Channel structs ---

// Channel maps the public.channels table.
type Channel struct {
	ID           string     `db:"id" json:"id"`
	Name         string     `db:"name" json:"name"`
	Logo         *string    `db:"logo" json:"logo,omitempty"`
	Category     *string    `db:"category" json:"category,omitempty"`
	EpgSourceID  *string    `db:"epg_source_id" json:"epg_source_id,omitempty"`
	EpgChannelID *string    `db:"epg_channel_id" json:"epg_channel_id,omitempty"`
	Status       string     `db:"status" json:"status"`
	CreatedAt    *time.Time `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt    *time.Time `db:"updated_at" json:"updated_at,omitempty"`
}

// Stream maps the public.streams table.
type Stream struct {
	ID          string     `db:"id" json:"id"`
	ChannelID   string     `db:"channel_id" json:"channel_id"`
	Label       string     `db:"label" json:"label"`
	URL         string     `db:"url" json:"url"`
	SourceType  string     `db:"source_type" json:"source_type"`
	DrmType     *string    `db:"drm_type" json:"drm_type,omitempty"`
	DrmK        *string    `db:"drm_k" json:"drm_k,omitempty"`
	LicenseURL  *string    `db:"license_url" json:"license_url,omitempty"`
	IsDefault   bool       `db:"is_default" json:"is_default"`
	Status      string     `db:"status" json:"status"`
	UserAgent   *string    `db:"user_agent" json:"user_agent,omitempty"`
	Referer     *string    `db:"referer" json:"referer,omitempty"`
	Resolution  *string    `db:"resolution" json:"resolution,omitempty"`
	Bitrate     *int       `db:"bitrate" json:"bitrate,omitempty"`
	CreatedAt   *time.Time `db:"created_at" json:"created_at,omitempty"`
}

// ChannelStream wraps a channel with its streams.
type ChannelStream struct {
	Channel `json:"channel"`
	Streams []Stream `json:"streams"`
}

// PlaylistWithChannels wraps a playlist with its channels.
type PlaylistChannelResult struct {
	Channel
	PlaylistName *string `db:"playlist_name" json:"playlist_name,omitempty"`
}

type Stats struct {
	TotalTitles    int `json:"total_titles"`
	TotalProviders int `json:"total_providers"`
	TotalGenres    int `json:"total_genres"`
}

func Connect() (*DB, error) {
	dsn := os.Getenv("ADOBOFLIX_PG_URL")
	if dsn == "" {
		dsn = os.Getenv("MPDUMPY_PG_URL")
	}
	if dsn == "" {
		return nil, fmt.Errorf("database URL not set — provide ADOBOFLIX_PG_URL or MPDUMPY_PG_URL")
	}

	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("database connection failed: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return &DB{db}, nil
}

func (db *DB) GetStats() (*Stats, error) {
	s := &Stats{}
	if err := db.QueryRow("SELECT COUNT(*) FROM vod_assets").Scan(&s.TotalTitles); err != nil {
		return nil, err
	}
	db.QueryRow("SELECT COUNT(DISTINCT source_type) FROM vod_assets WHERE source_type IS NOT NULL").Scan(&s.TotalProviders)
	db.QueryRow("SELECT COUNT(DISTINCT category) FROM vod_assets WHERE category IS NOT NULL AND category != ''").Scan(&s.TotalGenres)
	return s, nil
}

func (db *DB) GetEntries(provider, genre, contentType string, page, limit int) ([]Entry, int, error) {
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
	if err := db.QueryRow("SELECT COUNT(*) FROM vod_assets "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	entries := []Entry{}
	query := "SELECT " + entryColumns + " FROM vod_assets " + where +
		" ORDER BY created_at DESC NULLS LAST LIMIT $" + strconv.Itoa(len(args)+1) +
		" OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	if err := db.Select(&entries, query, args...); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

func (db *DB) GetEntry(id string) (*Entry, error) {
	var e Entry
	if err := db.Get(&e, "SELECT "+entryColumns+" FROM vod_assets WHERE id = $1", id); err != nil {
		return nil, err
	}
	return &e, nil
}

func (db *DB) Search(q, provider, genre, contentType string, page, limit int) ([]Entry, int, error) {
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
	if err := db.QueryRow("SELECT COUNT(*) FROM vod_assets "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	entries := []Entry{}
	query := "SELECT " + entryColumns + " FROM vod_assets " + where +
		" ORDER BY created_at DESC NULLS LAST LIMIT $" + strconv.Itoa(len(args)+1) +
		" OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	if err := db.Select(&entries, query, args...); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

func (db *DB) GetProviders() ([]string, error) {
	var p []string
	if err := db.Select(&p, "SELECT DISTINCT source_type::text FROM vod_assets WHERE source_type IS NOT NULL ORDER BY source_type"); err != nil {
		return nil, err
	}
	return p, nil
}

func (db *DB) GetGenres() ([]string, error) {
	var g []string
	if err := db.Select(&g, "SELECT DISTINCT category FROM vod_assets WHERE category IS NOT NULL AND category != '' ORDER BY category"); err != nil {
		return nil, err
	}
	return g, nil
}

// GetEpisodes returns all episodes for a given VOD asset, sorted by season/episode.
func (db *DB) GetEpisodes(vodID string) ([]Episode, int, error) {
	var episodes []Episode
	query := `SELECT id, vod_id, season_number, episode_number, name, stream_url,
		source_type, drm_type, drm_k, license_url, user_agent, referer
		FROM episodes WHERE vod_id = $1 ORDER BY season_number, episode_number`
	if err := db.Select(&episodes, query, vodID); err != nil {
		return nil, 0, err
	}

	// Count distinct seasons
	var seasons []int
	seasonQuery := `SELECT DISTINCT season_number FROM episodes WHERE vod_id = $1 ORDER BY season_number`
	if err := db.Select(&seasons, seasonQuery, vodID); err != nil {
		return episodes, 0, nil
	}
	return episodes, len(seasons), nil
}

// GetEpisode returns a single episode by ID.
func (db *DB) GetEpisode(episodeID string) (*Episode, error) {
	var e Episode
	query := `SELECT id, vod_id, season_number, episode_number, name, stream_url,
		source_type, drm_type, drm_k, license_url, user_agent, referer
		FROM episodes WHERE id = $1`
	if err := db.Get(&e, query, episodeID); err != nil {
		return nil, err
	}
	return &e, nil
}

// channelColumns lists columns for channel queries.
const channelColumns = `id, name, logo, category, epg_source_id, epg_channel_id,
	status, created_at, updated_at`

// streamColumns lists columns for stream queries.
const streamColumns = `id, channel_id, label, url, source_type, drm_type, drm_k,
	license_url, is_default, status, user_agent, referer, resolution, bitrate, created_at`

// ListChannels returns channels, optionally filtered by category.
func (db *DB) ListChannels(category string, limit, offset int) ([]Channel, int, error) {
	args := []interface{}{}
	where := "WHERE 1=1"
	if category != "" && category != "All" {
		where += " AND category = $" + strconv.Itoa(len(args)+1)
		args = append(args, category)
	}

	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM channels "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	channels := []Channel{}
	query := "SELECT " + channelColumns + " FROM channels " + where +
		" ORDER BY category NULLS LAST, name LIMIT $" + strconv.Itoa(len(args)+1) +
		" OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	if err := db.Select(&channels, query, args...); err != nil {
		return nil, 0, err
	}
	return channels, total, nil
}

// GetChannel returns a single channel by ID.
func (db *DB) GetChannel(channelID string) (*Channel, error) {
	var c Channel
	if err := db.Get(&c, "SELECT "+channelColumns+" FROM channels WHERE id = $1", channelID); err != nil {
		return nil, err
	}
	return &c, nil
}

// GetChannelWithStreams returns a channel and all its streams ordered by is_default DESC.
func (db *DB) GetChannelWithStreams(channelID string) (*Channel, []Stream, error) {
	c, err := db.GetChannel(channelID)
	if err != nil {
		return nil, nil, err
	}

	streams := []Stream{}
	query := "SELECT " + streamColumns + " FROM streams WHERE channel_id = $1 ORDER BY is_default DESC, bitrate DESC NULLS LAST"
	if err := db.Select(&streams, query, channelID); err != nil {
		return nil, nil, err
	}
	return c, streams, nil
}

// ListChannelCategories returns distinct channel categories.
func (db *DB) ListChannelCategories() ([]string, error) {
	var cats []string
	if err := db.Select(&cats, `SELECT DISTINCT category FROM channels WHERE category IS NOT NULL AND category != '' ORDER BY category`); err != nil {
		return nil, err
	}
	return cats, nil
}

// ResolveChannelStream returns the default stream for a channel, with fallback to the first active stream.
func (db *DB) ResolveChannelStream(channelID string) (*Stream, error) {
	var s Stream
	// Try default stream first
	query := "SELECT " + streamColumns + " FROM streams WHERE channel_id = $1 AND is_default = true LIMIT 1"
	if err := db.Get(&s, query, channelID); err == nil {
		return &s, nil
	}
	// Fallback: first active stream
	query = "SELECT " + streamColumns + " FROM streams WHERE channel_id = $1 AND status = 'active' LIMIT 1"
	if err := db.Get(&s, query, channelID); err == nil {
		return &s, nil
	}
	// Fallback: any stream at all
	query = "SELECT " + streamColumns + " FROM streams WHERE channel_id = $1 ORDER BY is_default DESC LIMIT 1"
	if err := db.Get(&s, query, channelID); err != nil {
		return nil, err
	}
	return &s, nil
}
