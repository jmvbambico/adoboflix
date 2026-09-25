package db

import (
	"fmt"
	"time"
)

// VodStream maps the public.vod_streams table from the AdoboTV PostgreSQL
// database — the authoritative per-asset stream list (vod_assets.stream_url
// is only a stale denormalized cache of the default row).
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

// vodStreamColumns lists the columns we explicitly select. Selecting "*" breaks
// because the table carries columns we intentionally do not surface (e.g.
// last_check, check_status) and sqlx errors on unmapped destinations.
const vodStreamColumns = `id, vod_id, label, url, source_type, drm_type, drm_k,
	license_url, is_default, status, user_agent, referer, resolution, bitrate,
	created_at`

// GetVodStreams returns every stream row for a VOD asset, ordered so the
// playable default comes first: is_default DESC (unique partial index
// guarantees at most one default), then online before any other status,
// then a stable tiebreak of bitrate DESC NULLS LAST and label.
func (db *DB) GetVodStreams(vodID string) ([]VodStream, error) {
	streams := []VodStream{}
	query := "SELECT " + vodStreamColumns + ` FROM vod_streams WHERE vod_id = $1
		ORDER BY is_default DESC,
			(status = 'online') DESC,
			bitrate DESC NULLS LAST,
			label`
	if err := db.Select(&streams, query, vodID); err != nil {
		return nil, fmt.Errorf("get vod_streams for vod %s: %w", vodID, err)
	}
	return streams, nil
}
