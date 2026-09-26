package source

import (
	"time"

	"github.com/lib/pq"
)

// The models below are the internal library format AdoboFlix's HTTP layer
// serialises. They are the shapes every adapter must produce, whichever
// protocol it speaks upstream: a database row, a JSON envelope or an M3U file
// all land here.
//
// The db tags are a pragmatic choice so a SQL-backed adapter can scan rows
// straight into these types without a second, drift-prone copy of every field.
// Non-SQL adapters ignore them.

// Entry is a VOD asset: a movie or a series.
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

// Episode is one episode of a series.
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

// Channel is a live TV channel.
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

// Stream is one source of a live channel.
type Stream struct {
	ID         string     `db:"id" json:"id"`
	ChannelID  string     `db:"channel_id" json:"channel_id"`
	Label      string     `db:"label" json:"label"`
	URL        string     `db:"url" json:"url"`
	SourceType string     `db:"source_type" json:"source_type"`
	DrmType    *string    `db:"drm_type" json:"drm_type,omitempty"`
	DrmK       *string    `db:"drm_k" json:"drm_k,omitempty"`
	LicenseURL *string    `db:"license_url" json:"license_url,omitempty"`
	IsDefault  bool       `db:"is_default" json:"is_default"`
	Status     string     `db:"status" json:"status"`
	UserAgent  *string    `db:"user_agent" json:"user_agent,omitempty"`
	Referer    *string    `db:"referer" json:"referer,omitempty"`
	Resolution *string    `db:"resolution" json:"resolution,omitempty"`
	Bitrate    *int       `db:"bitrate" json:"bitrate,omitempty"`
	CreatedAt  *time.Time `db:"created_at" json:"created_at,omitempty"`
}

// VodStream is one stream of a VOD asset. It is the authoritative per-asset
// stream list; Entry.StreamURL is only a stale denormalized cache of the
// default row.
type VodStream struct {
	ID         string     `db:"id" json:"id"`
	VodID      string     `db:"vod_id" json:"vod_id"`
	Label      string     `db:"label" json:"label"`
	URL        string     `db:"url" json:"url"`
	SourceType string     `db:"source_type" json:"source_type"`
	DrmType    *string    `db:"drm_type" json:"drm_type,omitempty"`
	DrmK       *string    `db:"drm_k" json:"drm_k,omitempty"`
	LicenseURL *string    `db:"license_url" json:"license_url,omitempty"`
	IsDefault  bool       `db:"is_default" json:"is_default"`
	Status     string     `db:"status" json:"status"`
	UserAgent  *string    `db:"user_agent" json:"user_agent,omitempty"`
	Referer    *string    `db:"referer" json:"referer,omitempty"`
	Resolution *string    `db:"resolution" json:"resolution,omitempty"`
	Bitrate    *int       `db:"bitrate" json:"bitrate,omitempty"`
	CreatedAt  *time.Time `db:"created_at" json:"created_at,omitempty"`
}

// Stats is the aggregate count of what the library holds.
type Stats struct {
	TotalTitles    int `json:"total_titles"`
	TotalProviders int `json:"total_providers"`
	TotalGenres    int `json:"total_genres"`
}
