# AdoboFlix

A local, self-hosted **player client for AdoboTV** with a Go backend, a React
frontend, and Shaka Player DRM support.

AdoboFlix plays content. It owns no content, no accounts, and no playlists, and
it is **read-only**: every query it issues against the AdoboTV database is a
`SELECT`.

There are two ways to use it:

- **AdoboTV subscriber** — connect your playlist and stream your entitled
  content through a modern web player.
- **No account** — point it at your own playlist (JSON or M3U) and get the same
  player.

Both paths feed one internal library format; the player does not care where the
content came from.

## Features

- 🎥 **Shaka Player** — HLS, MPEG-DASH, Widevine & ClearKey DRM
- 🎨 **Bento grid UI** — dark theme with amber accents
- 🔍 **Browse & search** — filter by provider, genre, and type
- 🎬 **Stream proxy** — rewrites and proxies HLS/DASH segments, forwarding DRM config
- 📺 **IPTV channels** — browse channels, resolve streams, and read EPG
- ⚡ **Go backend** — stdlib + Gin + sqlx

## Quick start

Prerequisites: Go 1.21+, Node.js 18+, and a PostgreSQL database (the AdoboTV
schema, or your own).

```bash
git clone https://github.com/jmvbambico/adoboflix.git
cd adoboflix
cp .env.example .env          # then set ADOBOFLIX_PG_URL

cd client && npm install && npm run build && cd ..

go build -o adoboflix ./cmd/server
./adoboflix --port 5656
```

Open **http://127.0.0.1:5656** in your browser.

## Configuration

Configuration is via environment variables (`.env`, documented in
`.env.example`):

| Variable | Default | Description |
|----------|---------|-------------|
| `ADOBOFLIX_PG_URL` | — | PostgreSQL connection string (required) |
| `MPDUMPY_PG_URL` | — | Fallback PG URL, used if `ADOBOFLIX_PG_URL` is unset |
| `SERVER_HOST` | `0.0.0.0` | Bind address |
| `SERVER_PORT` | `5656` | Port |

The CLI flags `--host` and `--port` override `SERVER_HOST` / `SERVER_PORT`.

## Data source

AdoboFlix reads the AdoboTV PostgreSQL schema — `vod_assets`, `episodes`,
`channels`, `streams`, `playlist_*`, and the `compiled_epg` blob. **AdoboTV owns
that schema**; this repository does not duplicate, migrate, or mutate it. See
the AdoboTV project for the authoritative table definitions. Queries select
explicit columns and are strictly read-only.

Source adapters are interchangeable and read-only:

- **`adobotv-http`** — production path; talks to AdoboTV over HTTP using the
  user's playlist code.
- **`file`** — a local playlist (JSON or M3U) for users with no account.
- **`postgres-direct`** — a development test harness only. It bypasses
  entitlement and analytics, must stay behind explicit configuration, and is
  never the default.

## API

All routes are served under `/api/v1` on the app's own origin.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/stats` | Library statistics |
| GET | `/api/v1/entries` | Paginated, filterable entries (`episode_count` for series) |
| GET | `/api/v1/entry/:id` | Single entry detail |
| GET | `/api/v1/search` | Title search with filters |
| GET | `/api/v1/providers` | Distinct providers |
| GET | `/api/v1/genres` | Distinct genres |
| GET | `/api/v1/resolve` | Resolve an entry's stream URL + DRM config |
| GET | `/api/v1/proxy` | HLS/DASH segment proxy |
| GET | `/api/v1/episodes/:vodId` | Episodes for a series, plus its season list |
| GET | `/api/v1/resolve/episode/:episodeId` | Resolve an episode's stream URL + DRM config |
| GET | `/api/v1/channels` | Paginated IPTV channels |
| GET | `/api/v1/channels/categories` | Distinct channel categories |
| GET | `/api/v1/channels/:id` | Channel with its streams |
| GET | `/api/v1/channels/:id/resolve` | Resolve a channel's default stream |
| GET | `/api/v1/channels/:id/epg` | EPG entries for a channel |
| POST | `/api/v1/channels/scan` | **Not implemented** — currently returns `501 Not Implemented` |

### Query parameters

- `provider` — filter by source
- `genre` — filter by category
- `type` — filter by content type
- `q` — search query
- `page` — page number (default: 1)
- `limit` — items per page (default: 200)

## Architecture

```
adoboflix/
├── cmd/server/main.go      # entry point, route table
├── internal/
│   ├── db/                 # read-only queries against the AdoboTV schema
│   ├── handler/            # HTTP handlers
│   ├── epg/                # XMLTV decode from compiled_epg (gzipped blob)
│   ├── scanner/            # stream health probing (reports only, never writes)
│   └── middleware/         # CORS
├── client/src/             # React 19 + Vite + Tailwind 4
│   └── components/CustomPlayer.tsx   # Shaka Player — HLS, DASH, DRM
├── static/                 # built client, served by the Go binary
├── Makefile
└── README.md
```

## Development

```bash
# Terminal 1 — backend
go run ./cmd/server

# Terminal 2 — frontend (hot reload)
cd client && npm run dev
```

The Vite dev server proxies `/api/*` requests to the Go backend on `:5656`.

## License

MIT — feel free to use, modify, and share.
