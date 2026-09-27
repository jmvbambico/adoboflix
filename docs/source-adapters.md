# Source adapters

AdoboFlix plays content. It does not own content. Everything it plays arrives
through a **source adapter**, and every adapter is strictly read-only.

This document is the contract. It was derived by reading AdoboTV's own
handlers, and every claim below was checked against the live schema or the
live `settings` table — the surprises are marked.

---

## The three adapters

| Adapter | For | Database | Status |
|---|---|---|---|
| `adobotv-http` | A subscriber with an AdoboTV account | none | The real path |
| `file` | Someone with no account and their own playlist | none | Supported |
| `postgres-direct` | Verifying the player itself | **required** | **Development harness only** |

Only `postgres-direct` needs `ADOBOFLIX_PG_URL`. Each adapter declares its need
at registration (`source.Register`) and the server opens a database connection
only when the selected one asks for it: `adobotv-http` and `file` read no SQL
handle at all and never dial one. That is the main practical reason to choose
`file` — the person it exists for has no AdoboTV account, and usually no AdoboTV
database either.

`postgres-direct` exists today and is what proved DASH+Clearkey playback works
end to end. It bypasses entitlement, device authorisation, and every analytics
event AdoboTV records. It must stay behind explicit configuration and must
never become the default. See [Why not the database](#why-not-the-database).

---

## `adobotv-http` — the whole flow hangs off one credential

The user supplies a **playlist code**. Nothing else. Everything downstream is
derived from it.

```
GET /v1/playlist/:code                       (JSON is the default format)
  -> GetUserByPlaylistCode
  -> status + expiry check
  -> User-Agent allowlist check
  -> detectAndAuthorizeDevice
  -> GenerateContentToken(userID, deviceID, key)
  -> the JSON envelope, with that token baked into every URL it contains
```

The response is **self-describing**: it carries the EPG URL and the VOD
library URL with the token already embedded. The adapter does not construct
those URLs itself — it follows them.

### The envelope

```json
{
  "provider": {
    "user_message": "Welcome to AdoboTV <username>!",
    "epg":          "<base>/v1/epg/adobotv.xml.gz?token=<contentToken>",
    "vod_library":  "<base>/v1/vod?token=<contentToken>",
    "billed_till":  "<unix seconds, or 0>"
  },
  "categories": {
    "<slug>": { "name": "Movies", "icon": "...", "adult": false }
  },
  "channels": [
    {
      "name":             "...",
      "category":         "<slug>",
      "icon":             "...",
      "epg_id":           "...",
      "url":              "<DECOY — see below>",
      "runtime_attr_url": "<base>/v1/drm/key/<encrypted>?token=<contentToken>"
    }
  ]
}
```

`billed_till` is only populated when the user's `plan_tier` is in the
`subscription_tiers` setting (live value: `Basic,Standard,Elite,Premium`).
Otherwise it is `0` — absence is not an error.

### `channels[].url` is a decoy — by design

It is the `channel_url_placeholder` setting, whose live value is
`https://www.youtube.com/watch?v=dQw4w9WgXcQ`.

**This is deliberate and must be preserved.** The real source is withheld from
the playlist envelope so that only a real client knows where to look, and
someone reading an intercepted or shared playlist cannot sniff out the upstream
CDN. The decoy is a security property of the platform, not an oversight.

Two consequences, and the second is the one that bites:

1. An adapter that reads `url` will play the decoy. The playable path is
   **always `runtime_attr_url`**.
2. **Do not "fix" this.** Putting a real URL into `channels[].url` — in
   AdoboTV or in any client that re-serialises a playlist — silently removes
   the protection. If a future change makes `url` look like a bug, it is not.
   Neither AdoboFlix nor any adapter may write a resolvable stream URL into
   that field, or into any playlist it exports.

### Resolving `runtime_attr_url`

```
GET <base>/v1/drm/key/<url-escaped AES blob>?token=<contentToken>
  -> 200 text/plain
  -> base64( ObfuscateJSON( details ) )
```

Decode base64, then parse as JSON. `ObfuscateJSON` escapes keys and values as
`\uXXXX` sequences, which Go's `encoding/json` unescapes natively — the
obfuscation is transparent to a standard parser. Do not write a de-obfuscator.

```json
{
  "drm_type":     "clearkey" | "widevine" | "playready" | "m3u",
  "drm_key":      "...",
  "url":          "<real CDN URL>",
  "user_agent":   "...",
  "referer":      "...",
  "content_id":   "<uuid>",
  "content_type": "channel" | "vod"
}
```

Semantics worth knowing, because they are not obvious:

- `drm_type` is **`"m3u"` when the content is not DRM-protected.** It is not a
  container hint and it does not mean HLS. Treat `m3u` as "no DRM".
- `drm_key` carries `drm_k` for ClearKey and `license_url` for everything
  else. One field, two meanings, selected by `drm_type`.
- If the stream URL embeds userinfo (`user:pass@`), has no license, and ends
  in `.mpd`, AdoboTV moves the credentials into `drm_key` as `user:pass` and
  strips them from the URL. Handle a `drm_key` in that shape.

### ⚠ The `m3u` output-format hazard

`/v1/drm/key/` branches on the user's **active playlist** `output_format`. If
that is `m3u`, the endpoint returns KODIPROP M3U text instead of the JSON
above. The adapter must detect that and fail with a clear message rather than
mis-parsing it. Playlist `format=m3u` is already rejected as deprecated at the
playlist endpoint, but the stored `output_format` can still be `m3u`, so this
is reachable.

### VOD library

```
GET <base>/v1/vod?token=<contentToken>    (the `vod_library` URL, followed verbatim)
```

Returns a **flat JSON array**, not an object:

```json
[
  {
    "category": "...",
    "name":     "...",
    "info": {
      "cast": [], "director": [], "country": [], "genre": [],
      "rating": "", "year": "", "added": "", "duration": "",
      "poster": "", "trailer": ""
    },
    "video":            "...",
    "runtime_attr_url": "...",
    "seasons": [
      { "season": 1, "episodes": [ { "episode": 1, "video": "...", "runtime_attr_url": "..." } ] }
    ]
  }
]
```

`seasons` present means a series; absent means a movie. Every playable leaf —
the asset itself and each episode — carries its own `runtime_attr_url`, and
each resolves exactly as above.

### EPG

```
GET <base>/v1/epg/adobotv.xml.gz?token=<contentToken>
```

Gzipped XMLTV. `internal/epg` already decodes gzipped XMLTV (today from the
`compiled_epg` blob), so the parsing is done — only the byte source changes.

---

## The three gates that will reject a real user

These are not edge cases. Each one produces a specific user-visible state that
the adapter must surface distinguishably, and getting this wrong looks like an
empty library or a crash.

### 1. Device approval — the first-connect experience

An unrecognised device does **not** get the playlist. It gets
`serveSplashM3U`. A new user's first connect lands in the operator's device
queue and stays on splash until approved.

**The splash response is the single worst trap in this API.** It is not an
error shape:

```
HTTP 200 OK
Content-Type: application/vnd.apple.mpegurl

#EXTM3U
#EXTINF:-1 tvg-id="AdoboTV" tvg-name="AdoboTV" ... ,AdoboTV
https://raw.githubusercontent.com/.../unauthorized.m3u
```

A `200`, with an M3U body, **even when the caller asked for JSON**. An adapter
that trusts the status code and calls `json.Unmarshal` gets a parse error and
reports "malformed playlist" — when the truth is "your device is waiting for
the operator to approve it."

So: on a `2xx` from `/v1/playlist/:code`, sniff the body before parsing. A body
whose first non-whitespace bytes are `#EXTM3U` while JSON was requested is the
pending-device signal, and must surface as **"device pending approval"** with
the operator's queue named as the next step. The splash URL is configurable
(`unauthorized_m3u_url`), so match on the `#EXTM3U` shape, never on that URL.

**Every first connect hits this path.** Device identity is
`ComputeDeviceHash(user.Email, GetDeviceIdentifier(userAgent), salt)`, and
`GetDeviceIdentifier` returns the **raw User-Agent string** unless it can
extract a hardware id from it. A new UA is therefore a new device. AdoboFlix
cannot avoid the queue on first run, and should not try to: presenting a UA
copied from an existing approved device to skip approval would defeat the
operator's device limits.

### 2. Account status — stricter for playback than for the playlist

The two checks disagree, deliberately:

| | Admits |
|---|---|
| `GET /v1/playlist/:code` | `active`, `inactive`, `expired` |
| `ContentAuthMiddleware` (everything playable) | **`active` only** |

So an `inactive` or `expired` user **can fetch a playlist and then fail to
play every item in it.** The adapter must report "subscription inactive"
rather than "playback failed", or every such user files a false bug.

Expiry is enforced separately and absolutely at the playlist endpoint, except
for `SUPERADMIN`.

### 3. User-Agent allowlist

`isValidUserAgent` does a `strings.HasPrefix` match against the
`valid_user_agents` setting. Live value:

```
OTT Navigator,OTT TV,OTT Player,Mozilla,Kodi,AdoboFlix
```

`Mozilla` is in the list, so any browser — and AdoboFlix's current Go proxy,
which sends a `Mozilla/5.0 (...)` UA — already passes. **This gate is not a
blocker.** `AdoboFlix` was added so AdoboFlix traffic is *distinguishable* in
audit logs and device records, not to unblock it. `format=m3u_native` skips
the check entirely.

---

## Verifying the HTTP adapter locally

AdoboTV can be run on this machine, so the adapter is verifiable end to end
rather than against fixtures alone. `~/projects/adobotv-server` is already
configured for it: `SERVER_ENV=development`, `SERVER_URL=http://127.0.0.1:8080`,
and `DATABASE_URL` pointing at the same local `127.0.0.1:5432/adobotv` database
AdoboFlix reads. The local library has 29 active users, all with playlist codes,
and 65 approved devices.

Three cautions, in order of how much they matter:

1. **Do not source that repo's `.env` wholesale.** It also holds
   `HEROKU_API_KEY` and `PRODUCTION_*` credentials. Pass an explicit minimal
   environment instead (`DATABASE_URL`, `ENCRYPTION_KEY`, `JWT_SECRET`,
   `DEVICE_HASHING_SALT`, `SERVER_PORT`, `SERVER_ENV=development`), and run no
   `heroku` or deploy command.
2. **AdoboTV writing to that database is expected and is not an AdoboFlix
   violation.** Serving a playlist updates `users.last_login`, may create a
   `devices` row, and records analytics — that is its job. The First Law binds
   AdoboFlix, not AdoboTV. Do not read a moved `last_login` as a breach; check
   what *AdoboFlix* issued.
3. **The HTTP adapter touches no database at all.** It is an HTTP client, so
   the read-only invariant is satisfied structurally. What needs verifying is
   envelope parsing, `runtime_attr_url` resolution, and the three gates above.

What can be verified without mutating the platform: the envelope fetch and
parse, the **pending-device** path (the first connect produces it by
construction), the **inactive/expired** path (a user whose status is not
`active` fetches a playlist and then fails every playable call), and a bad
playlist code. Reaching *content* requires an operator to approve the device
— one write to AdoboTV's `devices` table, which is the operator's call and not
the client's.

## `file` — a local playlist

For the second user in `AGENTS.md`: someone with **no AdoboTV account** who
already has a playlist and just wants AdoboFlix to play it. Point
`ADOBOFLIX_FILE_PATH` at a JSON file and the repo boots into the same player
the subscriber path uses — the player never learns where the content came
from.

The adapter reads the file **once, at startup**, into an in-memory library, and
every query answers from that value. It never writes the file, imports no SQL
package, and never touches the database — the server opens no connection for it
at all, and `source.Config.DB` arrives nil. The read-only invariants hold
structurally rather than by discipline. A path
that cannot be read, or bytes that are not the documented JSON, is a **startup
error** naming the path: AdoboFlix refuses to boot with a silently empty
library, because an empty player is indistinguishable from a broken one.

### The format is the internal models, serialised

There is no friendlier hand-authored format and no translation layer. The file
is a **direct serialisation of the Go types in `internal/source/models.go`** —
`Channel`, `Entry`, `Stream`, `VodStream`, `Episode` — in one object with five
arrays. The field names are the models' own `json` tags, which is why an
entry's provider is spelled `provider` (it is `Entry.SourceType`) while a
stream's is `source_type`.

```json
{
  "channels": [
    {
      "id": "news-one",
      "name": "News One",
      "category": "News",
      "logo": "https://img.example/news-one.png",
      "epg_channel_id": "news-one.tvg",
      "status": "active"
    }
  ],
  "entries": [
    {
      "id": "movie-a",
      "name": "Movie A",
      "type": "Movie",
      "category": "Films",
      "provider": "local",
      "status": "active",
      "poster": "https://img.example/movie-a.jpg",
      "cast_members": ["Actor One"],
      "directors": ["Director One"],
      "release_year": 2024,
      "created_at": "2024-01-01T00:00:00Z"
    },
    {
      "id": "series-b",
      "name": "Series B",
      "type": "Series",
      "category": "Shows",
      "provider": "local",
      "status": "active",
      "created_at": "2024-02-01T00:00:00Z"
    }
  ],
  "streams": [
    {
      "id": "news-main",
      "channel_id": "news-one",
      "label": "main",
      "url": "https://mycdn.example/news/index.m3u8",
      "source_type": "local",
      "is_default": true,
      "status": "active"
    }
  ],
  "vod_streams": [
    {
      "id": "movie-a-main",
      "vod_id": "movie-a",
      "label": "main",
      "url": "https://mycdn.example/movies/a/index.mpd",
      "source_type": "local",
      "drm_type": "Widevine",
      "license_url": "https://mycdn.example/license",
      "is_default": true,
      "status": "active"
    }
  ],
  "episodes": [
    {
      "id": "series-b-s01e01",
      "vod_id": "series-b",
      "season_number": 1,
      "episode_number": 1,
      "name": "Pilot",
      "stream_url": "https://mycdn.example/shows/b/s01e01.m3u8",
      "source_type": "local"
    }
  ]
}
```

Rules worth stating, because they are choices rather than accidents:

- **Every array is optional.** `{}` is a valid empty library.
- **Child rows point at their parent by id.** A `stream`'s `channel_id`, a
  `vod_stream`'s `vod_id` and an `episode`'s `vod_id` name the parent's id *as
  written in the file*.
- **Order in the file does not matter.** The adapter sorts every list it
  returns, because Go map iteration is random and an unsorted list would flap
  between calls.
- **`created_at` drives "newest first".** Entries with a timestamp come before
  those without (`NULLS LAST`), and ties break by name.

### How ids are derived

An id is **whatever the file provides** — `"news-one"`, a slug, a uuid. The
adapter never parses it; the interface treats it as an opaque, adapter-owned
token.

When a file omits one, the adapter synthesises a stable id so a URL a user
bookmarked still resolves after a reload. The shape is a truncated SHA-256 over
the item's identity fields, prefixed so the kinds stay apart:

| Kind | Prefix | Derived from |
|---|---|---|
| Channel | `ch-` | name, category |
| Entry | `vod-` | name, category |
| Stream | `str-` | channel_id, label, url |
| VodStream | `str-` | vod_id, label, url |
| Episode | `ep-` | vod_id, season_number, episode_number |

Derivation only helps **top-level** items, because a child references its
parent by an id the file spells out. A derived parent id cannot be referenced,
so a file that wants its streams or episodes grouped must give the parent an
id. An un-referenced child row is kept but unreachable through that parent.

### Capabilities

- **`StreamProbeLister` — supported.** A local playlist's live streams can be
  enumerated, so AdoboFlix can tell its user which of their own streams are
  dead. It is a report and nothing more: the adapter hands over a URL, the
  scanner redacts it to a host and a manifest before it reaches any report
  (`internal/scanner.RedactStreamURL`), and no result is ever persisted. Streams
  with no URL are skipped, and VOD streams and episodes are not enumerated —
  the report is about live channels.
- **`CompiledEPGProvider` — deliberately not supported.** A playlist file
  carries no gzipped XMLTV blob, so there is nothing honest to return. Leaving
  it unimplemented makes the EPG endpoint answer with
  `source.UnsupportedEPGError` and name why, instead of inventing an empty
  guide.

### Configuration

| Key | Required | Meaning |
|---|---|---|
| `ADOBOFLIX_FILE_PATH` | yes | Path to the JSON playlist. Unset or unreadable is a startup error. |

### Not implemented: M3U

These docs promise "JSON or M3U". Only the JSON form above is implemented
today; an M3U playlist is a separate piece of work. The loader parses bytes
into one in-memory library value and the query methods read that value, so an
M3U parser can be added later without touching any query code.

---

## Why not the database

`postgres-direct` is a test harness, and the reason is not hygiene. It is also
the only adapter that opens a database connection at all — the other two
declare no database need, so the server never dials one for them.

AdoboTV records a playback event at **both** `/v1/drm/key/` (channel or VOD
access, per content id and user) and `/v1/play/*`. Its README calls
`/v1/play/*` the "Playback Redirector — ensures all playback events are
captured before serving the stream."

A direct database tap bypasses all of it. Every AdoboFlix user on
`postgres-direct` is invisible to `top-channels`, `top-vods`, `traffic` and
`bandwidth`. Keeping it development-only is what keeps the operator's
analytics true.

`/v1/play/stream/:id`, `/v1/play/vod/:id` and `/v1/play/episode/:id` take a
UUID, record the event, and **302-redirect** to the real CDN URL. The redirect
target is a raw CDN URL that a browser cannot fetch directly (CORS, and a
`Referer` the CDN requires), so AdoboFlix still proxies — it follows the
redirect server-side and streams the body, exactly as `/api/v1/proxy` does
today.

---

## Stream URL exposure — what the browser can see

AdoboFlix is a **proxying client-side player**, and that shape has a
consequence worth stating plainly rather than rediscovering.

`/api/v1/resolve` hands the page a URL of the form
`…/api/v1/proxy?url=<upstream CDN URL>`, and `CustomPlayer.tsx` registers a
Shaka `registerRequestFilter` that rewrites **every** request — manifests and
segments alike — through that same parameter. So anyone with devtools open on
the page can read the real upstream CDN host. AdoboTV deliberately hides that
host (AES-encrypted inside `runtime_attr_url`, with the `channels[].url` decoy
on top — see "`channels[].url` is a decoy"), so the client does weaken a
property the platform enforces.

**What is fixed.** Credentials no longer travel with it. Some streams carry
HTTP Basic `user:pass`, which the adapter re-embeds in the stream URL's
userinfo (`injectUserInfo` in `internal/source/adobotvhttp/drm.go`). Resolve
now strips that userinfo before wrapping the URL and keeps it server-side
(`internal/handler/credentials.go`), keyed by origin; the proxy re-attaches it
as an `Authorization` header on the way out. The page never receives the
credentials, and because the key is the origin rather than the exact URL,
player-derived **segment** requests are authenticated too — which the old
userinfo-in-the-manifest-URL scheme never managed.

**What is fixed.** The server binds to `127.0.0.1` by default, so the
unauthenticated proxy and the resolve response are not offered to the LAN
unless someone sets `SERVER_HOST=0.0.0.0` on purpose.

**What remains.** The CDN host itself is still visible in `?url=`. This is
inherent to the design, not an oversight. Hiding it would take an opaque
handle **plus** a manifest-rewriting proxy — parse DASH and HLS, rewrite every
segment URI, variant playlist, `EXT-X-MAP` init segment and byte range, hold a
TTL map, handle live windows. An opaque handle for the manifest alone hides
nothing, because the player derives segment URLs from the manifest body and
the next segment request re-exposes the host.

That subsystem is not worth building for the threat that is actually present:
the only viewer is the subscriber, who already holds the playlist code, and
AdoboTV's decoy defends against a *leaked playlist* reaching a third party —
which does not transfer to a page only the subscriber loads. **Revisit it if
AdoboFlix becomes multi-user or internet-exposed**, at which point the proxy
needs authentication regardless.

---

## What this means for the interface

One constraint dominates the design: **the adapters do not agree on what a
content id is.**

| Adapter | Channel identity | VOD identity |
|---|---|---|
| `postgres-direct` | `channels.id` (uuid) | `vod_assets.id` (uuid) |
| `adobotv-http` | no id in the envelope — `name` + `epg_id`, with the real id sealed inside the encrypted blob | same |
| `file` | whatever the playlist provides | same |

So the interface must treat an id as an **opaque string owned by the adapter**
that produced it. It must not assume a UUID, must not parse it, and must not
round-trip it through anything that assumes UUID shape. An adapter is free to
use an encrypted blob, a name-derived key, or a uuid.

Every adapter method is a read. No adapter gets a write method, and no adapter
may persist anything upstream — that is the First Law in `AGENTS.md`, and it
applies to adapters without exception.

Health scanning stays a local advisory report regardless of adapter:
AdoboTV already owns the authoritative scan (`POST /v1/channels/scan`,
`POST /v1/vod/scan`, both permission-gated), and AdoboFlix never triggers or
persists it.
