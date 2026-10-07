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

Prerequisites: Go 1.21+ and Node.js 18+. A PostgreSQL database is needed only
for the `postgres-direct` development harness; login and local-playlist users
read no database.

```bash
git clone https://github.com/jmvbambico/adoboflix.git
cd adoboflix
cp .env.example .env          # then choose a source in the UI, or pin one

cd client && npm ci --include=dev && npm run build && cd ..

go build -o adoboflix ./cmd/server
./adoboflix --port 5656
```

`npm run build` writes the bundle straight into `static/`, which the Go binary
serves — there is no separate copy step, and `client/dist/` is not used. If the
environment sets `NODE_ENV=production`, install the build's devDependencies
with `npm ci --include=dev` first, as above.

Open **http://127.0.0.1:5656** in your browser.

## Configuration

Configuration is via environment variables (`.env`, documented in
`.env.example`):

| Variable | Default | Description |
|----------|---------|-------------|
| `ADOBOFLIX_SOURCE` | — | **Override.** Pins the source and wins. The only way to select `postgres-direct`. |
| `ADOBOFLIX_SOURCE_MODE_FILE` | `.adoboflix/source-mode` | Where the source mode chosen in the UI is remembered |
| `ADOBOFLIX_PG_URL` | — | PostgreSQL connection string (required by `postgres-direct`) |
| `MPDUMPY_PG_URL` | — | Fallback PG URL, used if `ADOBOFLIX_PG_URL` is unset |
| `ADOBOFLIX_ADOBOTV_BASE_URL` | — | AdoboTV base URL (required by `adobotv-http`) |
| `ADOBOFLIX_ADOBOTV_PLAYLIST_CODE` | — | Playlist code (optional; the UI can log in with a username/password, or enter a code directly) |
| `ADOBOFLIX_PLAYLIST_CODE_FILE` | `.adoboflix/playlist-code` | Where a UI-entered playlist code is stored (0600) |
| `ADOBOFLIX_PLAYLIST_REVALIDATE_INTERVAL` | `168h` | How long a stored playlist code is trusted before it is re-checked against AdoboTV. A re-check, not an expiry — see below. `0` disables it |
| `ADOBOFLIX_PLAYLIST_FILE` | `.adoboflix/playlist` | Where a UI-imported playlist is stored (0600); the extension is added per format |
| `ADOBOFLIX_FILE_PATH` | — | Playlist path used by `file` when nothing has been imported |
| `SERVER_HOST` | `127.0.0.1` | Bind address |
| `SERVER_PORT` | `5656` | Port |

The CLI flags `--host` and `--port` override `SERVER_HOST` / `SERVER_PORT`.

### Choosing a source

A source is chosen one of three ways, in this order:

1. **`ADOBOFLIX_SOURCE`** — the override. When set it pins the source and wins,
   and it is the only way to reach `postgres-direct`.
2. **A mode picked in the UI** — remembered in `ADOBOFLIX_SOURCE_MODE_FILE`.
   This is the normal path: **Login to AdoboTV** (the subscriber path) or
   **Import Local Playlist** (no account).
3. **Neither** — the server boots with **no source**. That is a valid state, not
   an error: `/api/v1/source/status` reports it and the UI offers the two
   choices. Until one is chosen every content route answers
   `409 source_not_configured`.

### The playlist code

For `adobotv-http`, the subscriber's playlist code can come from either place,
and **the file wins**:

- **Entered in the UI** — by signing in with an AdoboTV **username and
  password**, or by typing a **playlist code** directly. Either way the code is
  saved to `ADOBOFLIX_PLAYLIST_CODE_FILE` (mode `0600`).
- **`ADOBOFLIX_ADOBOTV_PLAYLIST_CODE`** — the fallback, used only when no code
  has been stored.

A UI-entered code is the user's most recent explicit instruction, so a stale
`.env` value must not silently override it. The server boots with no code at
all in this state, and the login screen supplies one while it runs — no
edit-and-restart. Clearing the stored code (DELETE `/api/v1/source/playlist-code`)
falls back to the environment variable again. The code is never logged and
never returned by any endpoint, not even masked.

### Logging in with a username and password

The **Login to AdoboTV** path takes a username and password, the same method the
AdoboTV client uses. The AdoboFlix server — never the browser — calls AdoboTV's
`/v1/auth/login`, then reads the account's `playlistCode` from `/v1/profile`, and
stores that code through the same store-and-swap path a typed-in code uses. So
the playlist code stays the only stored credential and everything downstream is
unchanged; the user simply never has to type a code.

- **The password is never persisted.** It lives for one request, is sent only to
  AdoboTV, and is never logged, stored, or echoed in a response. Only the
  playlist code obtained from the profile is kept.
- **A wrong username and a wrong password are reported identically**
  (`401 invalid_credentials`), because AdoboTV returns one `401` for both to
  prevent account enumeration — and AdoboFlix preserves that.
- **A reCAPTCHA demand is its own, distinct message** (`403 captcha_required`):
  if the AdoboTV server requires a captcha AdoboFlix cannot supply, the user is
  told so rather than being told their (correct) password was wrong.
- **A code still enters directly.** A user with a playlist code and no dashboard
  account keeps the playlist-code field, offered as a secondary option on the
  login form.

`ADOBOFLIX_ADOBOTV_BASE_URL` is required for this path too — it is the AdoboTV
server the login is made against.

### Importing a playlist

`POST /api/v1/source/playlist-file` accepts a playlist (the documented JSON
envelope, or M3U/M3U8) and stores it in `ADOBOFLIX_PLAYLIST_FILE`. It is parsed
by the real `file` adapter **before** anything is persisted, so a playlist that
does not parse is rejected with an error naming what was wrong, and neither the
file nor the mode is written. On success the source switches to `file`, and the
choice is remembered. `DELETE` clears the imported playlist; if it was the
active source, the server returns to the sourceless state above.

### The session: a weekly re-check, not an expiry

A playlist code entered in the UI is trusted for **one week**
(`ADOBOFLIX_PLAYLIST_REVALIDATE_INTERVAL`, default `168h`). When that window
lapses, the server re-checks the stored code against AdoboTV on the next boot.
This is **not** a session that expires: nothing upstream expires, and a code
that still works is re-confirmed silently, resetting the window, so the user
never sees it.

Only a **definitive rejection** — `playlist_rejected`, meaning AdoboTV refused
the code itself — ends the session and clears the stored code, returning the
user to the source chooser. Every other outcome leaves the credential exactly
where it is, because none of them says anything about whether the code is good:

| Re-check outcome | Session |
|---|---|
| the code works | kept, window reset |
| `device_pending`, `subscription_inactive`, `playlist_format_m3u`, `content_token_rejected`, `content_not_found` | kept — AdoboTV recognised the code; the gate is elsewhere |
| `playlist_rejected` | **ended** — the code itself was refused |
| `user_agent_rejected`, a malformed response, `upstream_error`, a timeout or a dead network | kept — inconclusive, so nothing is cleared |

Getting this backwards would silently destroy a user's credential on a network
blip, so the set of outcomes that prove a code invalid is kept as an explicit
list, not the negation of "proven valid". Set the interval to `0` to disable the
re-check; an environment-supplied code (`ADOBOFLIX_ADOBOTV_PLAYLIST_CODE`) has
no session at all.

### Sync

For the AdoboTV source, the library is cached for five minutes and refetched on
a read when stale. On top of that the server **refreshes once a day**, on the
first day change it observes after boot, so the first page load of the day is
warm. The check re-reads the calendar date rather than arming a timer for
midnight, so a laptop that sleeps across midnight refreshes when it wakes
instead of skipping the day. A failed refresh is logged and ignored — the
library is left as it was and the next read refreshes on the TTL — so it is
never an error state for the app.

When an AdoboTV source is active, the account menu shows **when it last synced**
and offers **Sync now** (`POST /api/v1/source/sync`). An imported playlist has
nothing to sync and no session, so neither is shown; its playlist is a snapshot
the user re-imports to update.

The bind address defaults to **loopback**. `/api/v1/proxy` is an
unauthenticated fetcher and `/api/v1/resolve` returns the upstream CDN URL, so
listening on every interface hands both to anything on the LAN. Set
`SERVER_HOST=0.0.0.0` only when you mean to expose AdoboFlix, and read
"Stream URL exposure" in `docs/source-adapters.md` before you do.

## Data source

AdoboFlix reads the AdoboTV PostgreSQL schema — `vod_assets`, `episodes`,
`channels`, `streams`, `playlist_*`, and the `compiled_epg` blob. **AdoboTV owns
that schema**; this repository does not duplicate, migrate, or mutate it. See
the AdoboTV project for the authoritative table definitions. Queries select
explicit columns and are strictly read-only.

Only the **`postgres-direct`** source needs `ADOBOFLIX_PG_URL`. Each adapter
declares its requirement at registration and the server opens a database
connection only when the selected one asks for it: `adobotv-http` and `file`
need no database and never dial one.

Source adapters are interchangeable and read-only. Two of them are the user
paths from `AGENTS.md`:

- **`adobotv-http`** — the subscriber path (**Login to AdoboTV**); talks to
  AdoboTV over HTTP using the user's playlist code. Needs no database.
- **`file`** — the no-account path (**Import Local Playlist**); a local playlist
  in JSON or M3U/M3U8. It is read once, never written, and touches no database.
  It can be imported through the UI or pointed at with `ADOBOFLIX_FILE_PATH`.
- **`postgres-direct`** — a development test harness only, and the only source
  that reads the database. It bypasses entitlement and analytics. It is **not a
  user choice**: the UI never offers it, and it is reachable only by setting
  `ADOBOFLIX_SOURCE=postgres-direct`. See `docs/source-adapters.md`.

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
| GET | `/api/v1/source/status` | Active source, its origin (env/stored/none), whether it is a dev harness, and the configured state of each mode (never the code) |
| POST | `/api/v1/source/login` | Log in to AdoboTV with a username/password, read the account's playlist code, and store it. The password is used for this one request and never persisted |
| POST | `/api/v1/source/playlist-code` | Validate a playlist code against the source, then persist it, select the login mode, and swap it in |
| DELETE | `/api/v1/source/playlist-code` | Clear the stored playlist code |
| POST | `/api/v1/source/playlist-file` | Validate a playlist (JSON or M3U), then persist it, select the file mode, and swap it in |
| DELETE | `/api/v1/source/playlist-file` | Clear the imported playlist |

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
