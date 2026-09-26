// Package db holds the PostgreSQL connection AdoboFlix opens against the
// AdoboTV database.
//
// This is a read-only relationship. Nothing in AdoboFlix — and nothing in an
// adapter — issues INSERT, UPDATE, DELETE, CREATE, DROP, ALTER or TRUNCATE
// against AdoboTV. See the First Law in AGENTS.md.
//
// The content reads that used to live here now live behind the source
// adapter boundary: see internal/source and internal/source/postgresdirect.
// What remains is the connection itself, which the postgres-direct adapter and
// the EPG service both borrow.
package db

import (
	"fmt"
	"os"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// DB is the AdoboTV database handle. It exposes the raw sqlx surface and no
// content queries: those belong to a source adapter, not to the connection.
type DB struct {
	*sqlx.DB
}

// Connect opens the configured AdoboTV database.
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
