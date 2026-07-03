package scanner

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

const (
	LocalDefaultPlaylistName = "Local Default"
	LocalDefaultPlaylistDesc = "Auto-generated local playlist. Channels verified playable via HTTP health check."
	ScanConcurrency          = 20 // parallel goroutines probing streams
	ScanTimeout              = 10 * time.Second
)

// StreamToCheck holds the data needed to test a stream.
type StreamToCheck struct {
	ChannelID   string `db:"channel_id"`
	StreamID    string `db:"id"`
	URL         string `db:"url"`
	SourceType  string `db:"source_type"`
	ChannelName string `db:"name"`
	Category    string `db:"category"`
}

// ScanResult summarizes the health check.
type ScanResult struct {
	TotalChannels int    `json:"total_channels"`
	AliveChannels int    `json:"alive_channels"`
	DeadChannels  int    `json:"dead_channels"`
	TotalStreams  int    `json:"total_streams"`
	AliveStreams  int    `json:"alive_streams"`
	DeadStreams   int    `json:"dead_streams"`
	PlaylistID   string `json:"playlist_id"`
	PlaylistName string `json:"playlist_name"`
}

// GetPlaylistID returns the ID of the Local Default playlist.
// Accepts *sqlx.DB directly so the handler can use it without importing db package.
func GetPlaylistID(database *sqlx.DB) (string, error) {
	var id string
	err := database.QueryRow(
		`SELECT id FROM playlists WHERE name = $1`, LocalDefaultPlaylistName,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("local playlist not found: %w", err)
	}
	return id, nil
}

// EnsureLocalPlaylist creates the "Local Default" playlist if it doesn't exist.
func EnsureLocalPlaylist(database *sqlx.DB) (string, error) {
	// Check if it already exists
	var id string
	err := database.QueryRow(
		`SELECT id FROM playlists WHERE name = $1`, LocalDefaultPlaylistName,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	// Create the playlist
	err = database.QueryRow(
		`INSERT INTO playlists (id, name, description, is_default, is_public, status, is_active)
		 VALUES (gen_random_uuid(), $1, $2, true, true, 'active', true)
		 RETURNING id`,
		LocalDefaultPlaylistName, LocalDefaultPlaylistDesc,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create local playlist: %w", err)
	}
	fmt.Printf("📡 Created playlist: %s (%s)\n", LocalDefaultPlaylistName, id)
	return id, nil
}

// FetchAllStreams returns all streams with their channel metadata for probing.
func FetchAllStreams(database *sqlx.DB) ([]StreamToCheck, error) {
	var streams []StreamToCheck
	query := `
		SELECT 
			s.channel_id, s.id, s.url, s.source_type,
			c.name, COALESCE(c.category, 'Unknown') as category
		FROM streams s
		JOIN channels c ON c.id = s.channel_id
		WHERE s.url IS NOT NULL AND s.url != ''
		ORDER BY c.name, s.is_default DESC
	`
	if err := database.Select(&streams, query); err != nil {
		return nil, fmt.Errorf("fetch streams: %w", err)
	}
	return streams, nil
}

// ProbeStream tests if a stream URL is accessible via HTTP.
func ProbeStream(ctx context.Context, streamURL string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", streamURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")

	client := &http.Client{
		Timeout: ScanTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// Read first 4KB to verify content is real
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)

	statusOk := resp.StatusCode >= 200 && resp.StatusCode < 400

	// For HLS/DASH, content matters more than status code
	if n > 0 {
		content := string(buf[:n])
		isValidStream := false
		if strings.Contains(content, "#EXTM3U") {
			isValidStream = true // HLS manifest
		} else if strings.Contains(content, "<MPD") || strings.Contains(content, "MPD") {
			isValidStream = true // DASH manifest
		} else if strings.Contains(content, "m3u8") || strings.Contains(content, ".m3u8") {
			isValidStream = true // HLS playlist reference
		} else if strings.Contains(content, "mpd") || strings.Contains(content, ".mpd") {
			isValidStream = true // DASH reference
		} else if strings.Contains(content, "ism") || strings.Contains(content, ".ism") {
			isValidStream = true // Smooth Streaming
		} else if n > 100 {
			// Has significant content — might be a binary stream
			isValidStream = statusOk
		}
		return statusOk && isValidStream
	}

	return statusOk
}

// StreamStatus represents the health status of a stream.
type StreamStatus struct {
	StreamID string
	Status   string // "online" or "offline"
}

// UpdateStreamStatuses updates the check_status on streams.
func UpdateStreamStatuses(database *sqlx.DB, statuses []StreamStatus) error {
	tx, err := database.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareNamed(
		`UPDATE streams SET check_status = :status, last_check = NOW() WHERE id = :streamid`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, s := range statuses {
		if _, err := stmt.Exec(s); err != nil {
			return fmt.Errorf("update stream %s: %w", s.StreamID, err)
		}
	}

	return tx.Commit()
}

// RebuildPlaylist clears and repopulates the playlist with only alive channel IDs.
func RebuildPlaylist(database *sqlx.DB, playlistID string, aliveChannels map[string]string) error {
	tx, err := database.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Clear existing entries
	if _, err := tx.Exec(`DELETE FROM playlist_channels WHERE playlist_id = $1`, playlistID); err != nil {
		return fmt.Errorf("clear playlist: %w", err)
	}

	// Insert alive channels in order
	stmt, err := tx.Prepare(
		`INSERT INTO playlist_channels (playlist_id, channel_id, category, display_order) VALUES ($1, $2, $3, $4)`,
	)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	order := 0
	for chID, category := range aliveChannels {
		order++
		if _, err := stmt.Exec(playlistID, chID, category, order); err != nil {
			return fmt.Errorf("insert channel %s: %w", chID, err)
		}
	}

	return tx.Commit()
}

// RunScan performs the full health check: probe every stream, update statuses, rebuild playlist.
func RunScan(database *sqlx.DB) (*ScanResult, error) {
	fmt.Println("📡 Starting channel health check scan...")

	// Ensure local playlist exists
	playlistID, err := EnsureLocalPlaylist(database)
	if err != nil {
		return nil, err
	}

	// Fetch all streams
	streams, err := FetchAllStreams(database)
	if err != nil {
		return nil, err
	}
	fmt.Printf("📡 Found %d streams to probe\n", len(streams))

	// Concurrently probe streams
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	type probeResult struct {
		StreamID  string
		ChannelID string
		Category  string
		Alive     bool
	}

	jobs := make(chan StreamToCheck, len(streams))
	results := make(chan probeResult, len(streams))

	// Launch workers
	var wg sync.WaitGroup
	for i := 0; i < ScanConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for stream := range jobs {
				alive := ProbeStream(ctx, stream.URL)
				results <- probeResult{
					StreamID:  stream.StreamID,
					ChannelID: stream.ChannelID,
					Category:  stream.Category,
					Alive:     alive,
				}
			}
		}()
	}

	// Send all streams to workers
	for _, s := range streams {
		jobs <- s
	}
	close(jobs)

	// Wait and close results
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	streamStatuses := []StreamStatus{}
	aliveChannels := map[string]string{} // channelID -> category
	totalStreams := 0
	aliveStreams := 0

	for r := range results {
		totalStreams++
		status := "offline"
		if r.Alive {
			status = "online"
			aliveStreams++
			aliveChannels[r.ChannelID] = r.Category
		}
		streamStatuses = append(streamStatuses, StreamStatus{
			StreamID: r.StreamID,
			Status:   status,
		})
	}

	// Update stream statuses in DB
	fmt.Printf("📡 Updating stream statuses (%d online, %d offline)\n", aliveStreams, totalStreams-aliveStreams)
	if err := UpdateStreamStatuses(database, streamStatuses); err != nil {
		return nil, fmt.Errorf("update statuses: %w", err)
	}

	// Rebuild playlist with only alive channels
	fmt.Printf("📡 Rebuilding playlist with %d alive channels\n", len(aliveChannels))
	if err := RebuildPlaylist(database, playlistID, aliveChannels); err != nil {
		return nil, fmt.Errorf("rebuild playlist: %w", err)
	}

	// Count unique channels
	allChannels := map[string]bool{}
	for _, s := range streams {
		allChannels[s.ChannelID] = true
	}

	result := &ScanResult{
		TotalChannels: len(allChannels),
		AliveChannels: len(aliveChannels),
		DeadChannels:  len(allChannels) - len(aliveChannels),
		TotalStreams:  totalStreams,
		AliveStreams:  aliveStreams,
		DeadStreams:   totalStreams - aliveStreams,
		PlaylistID:   playlistID,
		PlaylistName: LocalDefaultPlaylistName,
	}

	fmt.Printf("✅ Scan complete: %d/%d channels alive, %d/%d streams alive\n",
		result.AliveChannels, result.TotalChannels,
		result.AliveStreams, result.TotalStreams,
	)
	fmt.Printf("📡 Playlist '%s' rebuilt with %d channels\n", result.PlaylistName, result.AliveChannels)

	return result, nil
}
