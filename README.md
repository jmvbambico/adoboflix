![AdoboFlix](https://img.shields.io/badge/AdoboFlix-🎬-f59e0b?style=flat-square)

# AdoboFlix

**A self-hosted streaming media library server** with Go backend, React frontend, and Shaka Player DRM support.

Browse, search, and stream your personal VOD library with a modern Bento Grid UI inspired by OhMyProxy's dark amber theme.

> Forked from the original mpdumpy TugZ player — now standalone, open-source, and DRM-capable.

## Features

- 🎥 **Shaka Player** — Widevine & Clearkey DRM support
- 🎨 **Bento Grid UI** — modern, dark theme with amber accents
- 📚 **1,700+ titles** — browse by provider, genre, and type
- 🔍 **Full-text search** — instant title search with filters
- 🎬 **HLS/MPEG-DASH proxy** — stream rewriting for all sources
- ⚡ **Fast Go backend** — Gin + PostgreSQL, lightweight, sub-ms queries
- 🖼️ **Responsive** — works on desktop and mobile

## Quick Start

### Prerequisites

- Go 1.21+
- Node.js 18+
- PostgreSQL database with a `vod_assets` table

### 1. Clone & Build

```bash
git clone https://github.com/jmvbambico/adoboflix.git
cd adoboflix

# Build backend
go build -o adoboflix ./cmd/server

# Build frontend
cd client && npm install && npm run build && cd ..
```

### 2. Configure Database

Set your PostgreSQL connection string:

```bash
export ADOBOFLIX_PG_URL="postgres://user:pass@host:5432/dbname?sslmode=disable"
```

**Expected table schema:**

```sql
CREATE TABLE vod_assets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT,
    category TEXT,
    release_year TEXT,
    plot TEXT,
    poster TEXT,
    stream_url TEXT,
    drm_k TEXT,
    drm_type TEXT,
    source_type TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
```

### 3. Run

```bash
./adoboflix --port 5656
```

Open **http://127.0.0.1:5656** in your browser.

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/stats` | Dashboard statistics |
| GET | `/api/v1/entries` | Paginated entries with filters |
| GET | `/api/v1/entry/:id` | Single entry detail |
| GET | `/api/v1/search` | Full-text search |
| GET | `/api/v1/providers` | List all providers |
| GET | `/api/v1/genres` | List all genres |
| GET | `/api/v1/resolve` | Resolve stream URL with DRM config |
| GET | `/api/v1/proxy` | HLS/DASH stream proxy |

### Query Parameters

- `provider` — filter by source (Miruro, ReelPipe, etc.)
- `genre` — filter by category (Action, Drama, etc.)
- `type` — filter by content type (movie, tv)
- `q` — search query
- `page` — page number (default: 1)
- `limit` — items per page (default: 200)

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `ADOBOFLIX_PG_URL` | — | PostgreSQL connection string |
| `MPDUMPY_PG_URL` | — | Fallback PG URL (checked second) |

### CLI Flags

```bash
./adoboflix --host 0.0.0.0 --port 5656
```

## Architecture

```
adoboflix/
├── cmd/server/main.go      # Go entry point
├── internal/
│   ├── db/db.go            # PostgreSQL queries
│   ├── handler/handler.go  # HTTP handlers
│   └── middleware/         # CORS middleware
├── client/                  # React frontend
│   ├── src/
│   │   ├── App.jsx         # Main layout + bento grid
│   │   ├── components/
│   │   │   ├── ShakaPlayer.jsx  # DRM player
│   │   │   └── ...
│   ├── package.json
│   └── vite.config.js
├── static/                  # Built frontend
├── go.mod
├── Makefile
└── README.md
```

## Development

```bash
# Terminal 1 — Backend
go run ./cmd/server

# Terminal 2 — Frontend (hot reload)
cd client && npm run dev
```

The Vite dev server proxies `/api/*` requests to the Go backend on `:5656`.

## License

MIT — feel free to use, modify, and share.
