package db

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

type DB struct {
	*sqlx.DB
}

type Entry struct {
	ID          string     `db:"id" json:"id"`
	Name        string     `db:"name" json:"name"`
	Type        string     `db:"type" json:"type"`
	Category    *string    `db:"category" json:"category,omitempty"`
	ReleaseYear *string    `db:"release_year" json:"release_year,omitempty"`
	Plot        *string    `db:"plot" json:"plot,omitempty"`
	Poster      *string    `db:"poster" json:"poster,omitempty"`
	StreamURL   *string    `db:"stream_url" json:"stream_url,omitempty"`
	DrmK        *string    `db:"drm_k" json:"drm_k,omitempty"`
	DrmType     *string    `db:"drm_type" json:"drm_type,omitempty"`
	SourceType  string     `db:"source_type" json:"provider"`
	CreatedAt   *time.Time `db:"created_at" json:"created_at,omitempty"`
	UpdatedAt   *time.Time `db:"updated_at" json:"updated_at,omitempty"`
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
	db.QueryRow("SELECT COUNT(DISTINCT source_type) FROM vod_assets WHERE source_type IS NOT NULL AND source_type != ''").Scan(&s.TotalProviders)
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
		where += " AND LOWER(type) = LOWER($" + strconv.Itoa(len(args)+1) + ")"
		args = append(args, contentType)
	}

	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM vod_assets "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	entries := []Entry{}
	query := "SELECT * FROM vod_assets " + where + " ORDER BY created_at DESC NULLS LAST LIMIT $" + strconv.Itoa(len(args)+1) + " OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	if err := db.Select(&entries, query, args...); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

func (db *DB) GetEntry(id string) (*Entry, error) {
	var e Entry
	if err := db.Get(&e, "SELECT * FROM vod_assets WHERE id = $1", id); err != nil {
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
		where += " AND LOWER(type) = LOWER($" + strconv.Itoa(len(args)+1) + ")"
		args = append(args, contentType)
	}

	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM vod_assets "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	entries := []Entry{}
	query := "SELECT * FROM vod_assets " + where + " ORDER BY created_at DESC NULLS LAST LIMIT $" + strconv.Itoa(len(args)+1) + " OFFSET $" + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	if err := db.Select(&entries, query, args...); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

func (db *DB) GetProviders() ([]string, error) {
	var p []string
	if err := db.Select(&p, "SELECT DISTINCT source_type FROM vod_assets WHERE source_type IS NOT NULL AND source_type != '' ORDER BY source_type"); err != nil {
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
