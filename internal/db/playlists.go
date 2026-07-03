package db

import (
	"fmt"
)

// ListPlaylistChannels returns channels that belong to a playlist, filtered by category.
func (db *DB) ListPlaylistChannels(playlistID, category string, limit, offset int) ([]Channel, int, error) {
	var total int
	countQuery := `SELECT COUNT(*) FROM playlist_channels pc JOIN channels c ON c.id = pc.channel_id WHERE pc.playlist_id = $1`
	args := []interface{}{playlistID}
	argIdx := 2
	if category != "" {
		countQuery += fmt.Sprintf(` AND c.category = $%d`, argIdx)
		args = append(args, category)
		argIdx++
	}
	if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	channels := []Channel{}
	query := `SELECT c.id, c.name, c.logo, c.category, c.status, c.created_at, c.updated_at
		FROM playlist_channels pc
		JOIN channels c ON c.id = pc.channel_id
		WHERE pc.playlist_id = $1`
	queryArgs := []interface{}{playlistID}
	queryArgIdx := 2
	if category != "" {
		query += fmt.Sprintf(` AND c.category = $%d`, queryArgIdx)
		queryArgs = append(queryArgs, category)
		queryArgIdx++
	}
	query += fmt.Sprintf(` ORDER BY pc.display_order ASC NULLS LAST, c.name LIMIT $%d OFFSET $%d`, queryArgIdx, queryArgIdx+1)
	queryArgs = append(queryArgs, limit, offset)
	if err := db.Select(&channels, query, queryArgs...); err != nil {
		return nil, 0, err
	}
	return channels, total, nil
}

// ListPlaylistCategories returns distinct categories from channels in a playlist.
func (db *DB) ListPlaylistCategories(playlistID string) ([]string, error) {
	var cats []string
	query := `SELECT DISTINCT c.category FROM playlist_channels pc
		JOIN channels c ON c.id = pc.channel_id
		WHERE pc.playlist_id = $1 AND c.category IS NOT NULL AND c.category != ''
		ORDER BY c.category`
	if err := db.Select(&cats, query, playlistID); err != nil {
		return nil, err
	}
	return cats, nil
}

// GetDefaultPlaylistID returns the ID of the Local Default playlist.
func (db *DB) GetDefaultPlaylistID() (string, error) {
	var id string
	err := db.Get(&id, `SELECT id FROM playlists WHERE is_default = true AND name = 'Local Default' LIMIT 1`)
	if err != nil {
		return "", nil // Not found — scan needed
	}
	return id, nil
}

// ListPlaylistChannelIDs returns channel IDs in a playlist (for the scanner to rebuild).
func (db *DB) ListPlaylistChannelIDs(playlistID string) ([]string, error) {
	var ids []string
	if err := db.Select(&ids, `SELECT channel_id FROM playlist_channels WHERE playlist_id = $1 ORDER BY display_order`, playlistID); err != nil {
		return nil, err
	}
	return ids, nil
}
