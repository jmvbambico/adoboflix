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
| `adobotv-http` | A subscriber with an AdoboTV account | none | The real path — **Login to AdoboTV** |
| `file` | Someone with no account and their own playlist | none | Supported — **Import Local Playlist** |
| `postgres-direct` | Verifying the player itself | **required** | **Development harness only, not a user choice** |

Only `postgres-direct` needs `ADOBOFLIX_PG_URL`. Each adapter declares its need
at registration (`source.Register`) and the server opens a database connection
only when the selected one asks for it: `adobotv-http` and `file` read no SQL
handle at all and never dial one. That is the main practical reason to choose
`file` — the person it exists for has no AdoboTV account, and usually no AdoboTV
database either.

The first two are the two use cases in `AGENTS.md`, and they are the two
**selectable** adapters: they are what the UI offers. `postgres-direct` is
marked `Dev` and deliberately **not** selectable. It is reachable only through
`ADOBOFLIX_SOURCE=postgres-direct` — which is now the *only* route to it, so it
stays behind explicit configuration more firmly than before.

`postgres-direct` exists today and is what proved DASH+Clearkey playback works
end to end. It bypasses entitlement, device authorisation, and every analytics
event AdoboTV records. See [Why not the database](#why-not-the-database).

## How the active source is chosen

`ADOBOFLIX_SOURCE` is an **override**, not the only way in. The order is:

| Source of the choice | Used when |
|---|---|
| `ADOBOFLIX_SOURCE` | it is set — it pins the source and wins, and it is the only way to select a dev adapter |
| the mode the user chose in the UI, remembered in `ADOBOFLIX_SOURCE_MODE_FILE` (default `.adoboflix/source-mode`) | `ADOBOFLIX_SOURCE` is unset and a mode is stored |
| neither | the server boots with **no source** |

Booting with no source is a **valid state, not an error**. It is the opposite
of a silent fallback: `/api/v1/source/status` reports it, the UI offers the two
selectable modes, and every content route answers `409 source_not_configured`
until one is chosen. Only a `Selectable` adapter can be remembered as a mode; a
hand-edited file naming a dev adapter is refused, not opened.

The remembered mode is the user's most recent explicit instruction, so it wins
over nothing but the override — the same precedence shape the playlist code
uses. A malformed mode file is a startup error, never a silent default.

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

## Choosing a mode at runtime

The two selectable paths can be chosen while the server runs, instead of editing
`.env` and restarting:

- **Login to AdoboTV** — enter a playlist code (`POST
  /api/v1/source/playlist-code`). Entering a code **selects the `adobotv-http`
  mode** whether the server is sourceless, already on `adobotv-http`, or on
  `file`.
- **Import Local Playlist** — upload a playlist (`POST
  /api/v1/source/playlist-file`). Importing **selects the `file` mode**.

Both are refused with `409 source_pinned_by_env` when `ADOBOFLIX_SOURCE` pins a
different source: the override is the only way to a dev adapter, so the UI must
not fight it.

The playlist code arrives through the API while the server runs and is stored in
a **local file the server owns** — never in the browser, and never upstream.
`ADOBOFLIX_ADOBOTV_PLAYLIST_CODE` is not the only way to supply it.

### Precedence — the file wins

| Source | Used when |
|---|---|
| `ADOBOFLIX_PLAYLIST_CODE_FILE` (default `.adoboflix/playlist-code`, mode `0600`) | it holds a code |
| `ADOBOFLIX_ADOBOTV_PLAYLIST_CODE` | no code has been stored |

A code entered in the UI is the user's most recent explicit instruction, so a
stale `.env` value must not override it; silently doing so would make the UI
look broken. With **neither** present the source is *unconfigured*: the server
boots (deliberately, so a new subscriber can supply the code),
`/api/v1/source/status` reports `playlist_code_configured: false`, and every
content route answers `403` with code `playlist_code_required` until a code is
entered. Clearing the stored code falls back to the environment variable again.

### The endpoints

```
GET    /api/v1/source/status
  -> { "source": "adobotv-http" | "file" | "",   # active adapter, "" when none
       "active": bool,
       "origin": "env" | "stored" | "none",
       "dev": bool,                              # a harness the UI hides
       "needs_playlist_code": bool,
       "playlist_code_configured": bool,
       "playlist_file_configured": bool,
       "modes": [ { "name", "selectable", "dev",
                    "active", "configured", "needs_playlist_code" } ] }
       # never the code, and the account facts when the adapter can supply them

POST   /api/v1/source/playlist-code    {"code": "..."}
DELETE /api/v1/source/playlist-code
POST   /api/v1/source/playlist-file    <the playlist content: JSON or M3U>
DELETE /api/v1/source/playlist-file
```

`status` is what the menu renders from, so the client never has to hardcode
adapter names: `modes` lists every registered adapter with the flags to decide
what to offer (`selectable`, `dev`) and whether it is ready to open
(`configured`). `origin` says whether the active source came from the
environment override or a stored choice, so the UI knows when the choice is not
its to make. It stays fast, local and cancellable: the account facts are read
from cache only, never fetched.

`POST` **validates before persisting**: it opens a candidate adapter and makes
one cheap real call — the playlist envelope — so a wrong code fails here rather
than on the user's first playback attempt. Whether the code is *kept* is then
decided by whether AdoboTV recognised it, not by whether the call merely
succeeded: a gate that runs after the code is resolved keeps the code, and a
rejection or an inconclusive failure writes nothing. See "Which failures keep
the code" below. Either way the code itself is never echoed, and the same gate
code the rest of the API returns is returned — `playlist_rejected`,
`device_pending`, `subscription_inactive`, `user_agent_rejected` and friends.
The client already branches on those; no parallel vocabulary is introduced.

`DELETE` clears the stored code and reopens the adapter from what remains (the
environment fallback, or the unconfigured state). Clearing nothing is not an
error. The chosen mode is left alone — the user still wants the login path,
they have simply removed its credential.

### Importing a playlist file

`POST /api/v1/source/playlist-file` takes the playlist content as the request
body and **validates it before persisting anything**, exactly as the
playlist-code endpoint does: the bytes are written to a scratch file and parsed
by the **real `file` adapter**. Only once they parse does the store persist the
playlist under `ADOBOFLIX_PLAYLIST_FILE` (default `.adoboflix/playlist`, mode
`0600`, atomic temp+rename, read back to verify), the mode is remembered, and
the live source is swapped to `file`. On any failure **nothing is persisted and
nothing is swapped**.

Both formats the adapter supports are accepted: the JSON envelope above, and
M3U/M3U8. The server must name the stored file, and the adapter chooses its
parser by extension, so the extension is picked from the content's shape (`{` /
`[` → `.json`; a `#` directive or `#EXTM3U`/`#EXTINF` → `.m3u`). That is a
two-way choice between the adapter's only two formats, not a third parser: a
wrong guess still fails loudly in the adapter's own parser.

**The rejection is the feature.** A user who got the format wrong must learn
*what* was wrong, so a malformed upload returns `400 invalid_playlist` with the
parser's own reason, the format it tried, and a pointer to the documented
format:

```json
{
  "error": "the playlist could not be parsed as JSON: unexpected end of JSON input. See the documented format in docs/source-adapters.md.",
  "code": "invalid_playlist"
}
```

The request body is capped at **10 MiB** (`413 playlist_too_large`): parsing is
in-memory, and an unbounded upload on a loopback service is a denial of service
worth refusing.

`DELETE /api/v1/source/playlist-file` clears the imported playlist. If it was
the active source, the remembered mode is cleared with it and the server returns
to the sourceless state — a stored mode pointing at a file that is gone would
otherwise fail to open on the next boot. An env-pinned source is left running:
it reads `ADOBOFLIX_FILE_PATH`, not the imported file.

### Which failures keep the code

`POST` keeps the code when it has **proved itself valid** — when AdoboTV
recognised it and gated access for some other reason — and drops it otherwise.
That split exists so a first-time subscriber whose device is awaiting approval
does not have to retype a correct code after the operator approves it. The
question is not "did the call succeed" but "did the code identify the
subscriber". When the code is kept the gate is still returned, so the client
shows the right screen; reads fail with that gate until it clears, then start
working with no further user action.

| Validation failure | Code kept? | Why |
|---|---|---|
| (success) | yes | — |
| `device_pending` | **yes** | the device gate runs *after* the code is resolved, so the code was accepted; it clears on operator approval with no re-entry. |
| `subscription_inactive` | **yes** | the playlist endpoint admits inactive/expired accounts, so the refusal is the account state, not the code. |
| `playlist_format_m3u` | **yes** | the endpoint answered with this account's own playlist, so the code was accepted; the m3u format is an upstream account setting. |
| `content_token_rejected` | **yes** | the playlist was served (the code was accepted) and only a minted token lapsed. |
| `content_not_found` | **yes** | the playlist was served; only a specific item was missing. |
| `playlist_rejected` | no | the code itself was refused. |
| `user_agent_rejected` | no (ambiguous) | it is unclear whether the allowlist is checked before or after the code lookup, so it does not reliably prove the code valid; its remedy is a UA change, not re-entry. |
| `malformed_playlist`, `malformed_drm`, `malformed_vod_library` | no | an unparseable 2xx body proves nothing about the code. |
| `upstream_error` | no | an unreachable or failing upstream has said nothing about the code. |
| `playlist_code_required`, anything unknown | no | default: when in doubt, do not persist. A code the user must re-enter is a smaller harm than a bad code sticking and failing every later request. |

Only success and the `device_pending`, `subscription_inactive`,
`playlist_rejected`, `user_agent_rejected`, `playlist_format_m3u`,
`malformed_playlist` and `upstream_error` failures are reachable through the
current validation call (a single playlist-envelope fetch). The rest are
classified anyway so the rule is complete and survives a change to what
validation exercises.

### Known limits

- **A swap does not refresh the health-scan manager.** `scanManager` builds its
  `scanner.Manager` once under a `sync.Once`, from the source that was active at
  the first scan, so a later playlist-code swap leaves it pointing at the
  previous adapter. That is currently harmless: `adobotv-http` is not a
  `StreamProbeLister`, so a scan against it is unsupported either way and no
  manager is ever built for it, while the sources that *are* probe-listable
  (`file`, `postgres-direct`) take no playlist code and never swap. Whoever makes
  `adobotv-http` probe-listable must refresh the manager on swap first,
  otherwise a scan would run against a stale credential.

### The credential is write-only

### How the swap is guarded

The active source sits behind an atomic pointer. Reads are hot and swaps are
rare, so the load path is a single atomic operation with no lock, and an
in-flight request keeps whichever source it loaded — a swap can never race it.
`PlayerHandler` reads the source only through one accessor, so a swap reaches
every content route at once. Entering a code is the only thing that swaps; when
the active source takes no playlist code (`file`, `postgres-direct`), `POST`
answers `409 playlist_code_not_supported` and nothing is swapped.

### The credential is write-only

The code this API accepts is the same credential that must never reach the page
(see "Stream URL exposure"). So it is never returned by any endpoint — not in a
body, not masked; never logged (at most its length is); never included in an
error message; written mode `0600`, atomically (temp file + rename); and stored
in a local file rather than the browser. Keeping it in `localStorage` and
sending it per request is explicitly rejected, because that puts the credential
back in the page and undoes the work that took it off.

Persisting it is a **local** write, not an upstream one. The First Law binds
AdoboFlix against the AdoboTV database; a file the server owns, holding the
user's own credential, is not a write to AdoboTV. No adapter gained a write
method: the code is supplied to `source.Open` through an additive
`source.Config.PlaylistCode` field and the adapter is reopened, exactly as it is
at startup.

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
`ADOBOFLIX_FILE_PATH` at a local playlist — JSON or M3U — and the repo boots
into the same player the subscriber path uses, or import the playlist through
the UI, which stores it under `ADOBOFLIX_PLAYLIST_FILE` and selects this mode.
An explicit path (the imported one) wins over `ADOBOFLIX_FILE_PATH`, the same
precedence shape the playlist code uses. The player never learns where the
content came from.

The adapter reads the file **once, when it is opened** — at startup, or when an
import swaps it in — into an in-memory library, and
every query answers from that value. It never writes the file, imports no SQL
package, and never touches the database — the server opens no connection for it
at all, and `source.Config.DB` arrives nil. The read-only invariants hold
structurally rather than by discipline. A path
that cannot be read, or bytes that are not the format its extension selects, is
a **startup error** naming the path: AdoboFlix refuses to boot with a silently
empty library, because an empty player is indistinguishable from a broken one.

### Format is chosen by extension

The file's extension selects the parser, and nothing else does:

| Extension | Parser |
|---|---|
| `.json` | the JSON envelope below |
| `.m3u`, `.m3u8` | the M3U playlist below |

Extension, not content-sniffing, on purpose. A body can be valid in both
formats and a file can be misnamed; if the parser were chosen by looking at the
bytes, a malformed `.m3u` might be quietly retried as JSON and the user would
get a puzzling error about the wrong format. Choosing by extension means a
malformed `.m3u` reports as a malformed M3U and a malformed `.json` as a
malformed JSON. Any other extension (or none) is a startup error naming the
supported set: `.json, .m3u, .m3u8`.

The AdoboTV `m3u` output-format hazard documented above is a different thing and
does not bear on this parser: it is about an HTTP response body from
`/v1/drm/key/` that was requested as JSON, not about a file a user points the
adapter at. A user's `.m3u` file does carry KODIPROP-style `#` directives, and
this parser ignores every directive it does not use — but the file's format is
decided by its extension before any of that, so there is no ambiguity to
resolve.

### The JSON format is the internal models, serialised

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
- **Values are compared as trimmed text.** A category written `" Films "` is
  exposed *and filtered* as `Films`, and genres, providers and channel
  categories are listed once per case-insensitive value — so the list never
  advertises something the filter would fail to match. Ids and the
  `channel_id`/`vod_id` references are trimmed the same way, so
  `"  ch-padded  "` is looked up (and listed) as `ch-padded`.

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
| Episode | `ep-` | vod_id, season_number, episode_number, name, stream_url |

The episode derivation includes the name and stream URL on purpose: a playlist
may list episodes without season/episode numbers, and those unmarshal to `0`.
Hashing only the numbers would give every such episode the same id and the
loader would drop all but the first. Including enough identity keeps rows that
genuinely differ apart.

Ids are assigned in **two passes**, so file order never changes which rows
exist: every id the file spells out is reserved first, and a derived id is
drawn from outside that set. An explicit id therefore always wins over a
derived one, wherever the two sit relative to each other. A derived id that
would still collide with another derived id (the two rows are identical in
every field the derivation reads) is suffixed (`…-2`, `…-3`), following file
order so it stays stable across reloads, rather than dropping a genuinely
distinct row.

A row that repeats an id the file has already given an earlier row of the same
kind is ambiguous: for channels, entries and episodes the later one is skipped.
Streams and `vod_streams` are the exception — every row is kept, because two
rows under one channel are two streams and nothing looks a stream up by id.
Their derived ids still avoid every id the file provides.

Derivation only helps **top-level** items, because a child references its
parent by an id the file spells out. A derived parent id cannot be referenced,
so a file that wants its streams or episodes grouped must give the parent an
id. An un-referenced child row is kept but unreachable through that parent.

### The M3U format

An M3U/M3U8 playlist is an `#EXTM3U` header followed by `#EXTINF` entries, each
with the URL it plays on the next line:

```
#EXTM3U
#EXTINF:-1 tvg-id="news.tvg" tvg-name="News One" tvg-logo="https://img.example/news.png" group-title="News",News One
https://mycdn.example/live/news/index.m3u8
#EXTINF:-1 group-title="Series",Breaking Bad S01E02
https://mycdn.example/shows/bb/s01e02.mkv
```

What the parser accepts, and what it drops:

- **Accepted**, because real playlists do it: attributes in any order; values in
  double quotes, single quotes, or bare; unknown attributes (`catchup`,
  `timeshift`, …) and unknown `#` directives; blank lines and comments; CRLF
  endings; a UTF-8 BOM; and a missing `#EXTM3U` header.
- **Dropped, and counted** in the startup summary: an `#EXTINF` whose next line
  is not a URL (or that runs to end of file), and a bare URL line with no
  preceding `#EXTINF` — the latter has no title and no group, so there is nothing
  honest to file it as.
- **Recognised but ignored**: every `#` directive the parser does not use,
  including `#KODIPROP`, `#EXTGRP` and `#EXT-X-*`.
- A file with neither an `#EXTM3U` header nor any `#EXTINF` is not a playlist
  and is a **startup error** — this is what makes a JSON file renamed `.m3u`
  report as a malformed M3U rather than loading as an empty library.

Attributes map onto the internal models: `tvg-logo` → channel `logo`, `tvg-id`
→ `epg_channel_id`, and `group-title` → the channel `category` or the VOD
category. `tvg-name` is the channel name only when the display title is empty.
Every row's provider is `file`, and each live channel gets one default stream.

Every M3U id is **derived**, because the format carries none: the same `ch-` /
`vod-` / `str-` / `ep-` derivation and the same two-pass reservation the JSON
path uses (see above), so reloading the same file yields the same ids, episodes
of one series share their series' id, and two distinct rows whose derivation
inputs are identical are suffixed rather than merged.

#### Classification: group heuristics, and the guesswork that comes with them

M3U carries no type, no ids and no parent/child links, so the library is
**inferred** rather than translated. The rule is:

> An entry is VOD when its `group-title` contains one of `movie`, `movies`,
> `film`, `films`, `vod`, `series`, `show`, `shows` as a whole word — matched
> case-insensitively and with surrounding whitespace ignored. Everything else is
> a live channel.

Whole-word matching means `Showtime` is a channel and `TV Shows` is VOD. The
word list is **English-only**, deliberately: a group named in another language
falls through to live channels. That is a stated limitation, not an oversight —
widening the list (or making it configurable) is the owner's call, and the
startup summary below names exactly which groups were treated as VOD so the
fall-through is visible.

This is a heuristic and it is expected to **misfile some content**. A film whose
group is spelled in another language becomes a channel, and a series grouped
under `Documentaries` (no keyword) does too. A user who cannot find a title
should be able to read the summary log and see whether it was filed as a
channel. Config-declared classification was considered and rejected, so there is
no override — the log is the whole visibility story.

#### Season and episode are parsed from titles

Within a VOD group, a title is scanned for a season and episode number:

| Shape | Example |
|---|---|
| `SxxExx` | `Breaking Bad S01E02` |
| `NxNN` | `Breaking Bad 1x02` |
| `Season N Episode M` | `Breaking Bad Season 1 Episode 2` |

All are matched case-insensitively (`s01e02`, `SEASON 1 EPISODE 2`), and
`Season N Ep M` is accepted too.

- A title with a parseable season/episode becomes an **episode** of a series.
  The marker divides the title: the text **before** it, separators trimmed, is
  the series name, and the text after it is the episode's own title. So
  `Breaking Bad S01E01 - Pilot` is episode `Pilot` of `Breaking Bad`, and
  `Breaking Bad S01E02 - Cat's in the Bag` is another episode of the *same*
  series. Episodes that share a series name and group land under **one** VOD
  entry, with type `Series` and one shared id; an episode with no trailing text
  keeps the full title as its name.
- A VOD-classified entry whose title has **no** parseable season/episode is
  still a VOD row, filed as a `Movie` with a single default `vod_stream` — it is
  never dropped just because the title could not be parsed.

When the title carries no series prefix (`1x02 - The Dundies`), the series name
comes from elsewhere, in this order:

1. the text before the marker in the title (the case above — it wins);
2. the `tvg-name` attribute, when it is present and reduces to a non-empty name;
3. the group name, as a last resort.

`tvg-name` is often the *entry* name rather than the series — the whole
`Breaking Bad S01E01`, or the episode title — so the same marker split is
applied to it: a `tvg-name` of `Breaking Bad S01E01` reduces to `Breaking Bad`,
and a `tvg-name` that is only a marker (`S01E01`) reduces to nothing and falls
through to the group. That is what tells two prefixless shows sharing one group
— `The Office US` and `Fawlty Towers`, each in `group-title="Series"` — apart,
while keeping a marker-carrying `tvg-name` from splitting one show into one
entry per episode. Series names are compared case- and whitespace-insensitively,
so `The Office US` and `the  office us` are one series.

#### What the startup summary reports

Because the classification is guesswork, the adapter logs what it decided, one
line per category, when the server boots:

```
[source] m3u: read 1234 entries from /path/playlist.m3u (dropped 6: 5 #EXTINF without a URL, 1 bare URLs without #EXTINF)
[source] m3u: 1180 live channels
[source] m3u: 49 VOD rows across 8 series
[source] m3u: series identity: 5 from title, 2 from tvg-name, 3 from group name
[source] m3u: 12 VOD titles had no parseable season/episode (filed as movies)
[source] m3u: VOD group names: Movies, Series, TV Shows
```

Read top to bottom: entries read and dropped, how many became channels, how many
became VOD and across how many series, where each series got its identity, how
many VOD titles had no season/episode (filed as movies), and which group names
were treated as VOD. A film missing from the VOD list was almost certainly
counted as a channel here. The **from group name** count is the one that tells a
user their playlist lacks the metadata to group reliably: every series counted
there was named after its group, so two different prefixless shows in one group
will have been merged under that group's name. A JSON playlist has no inference
to report and logs none of this.

#### Known limits

The mapping is a heuristic and four things are known to be lossy. None is a bug
to file:

- **Two shows with the same name and no distinguishing metadata cannot be
  separated.** If both the title and `tvg-name` say only `The Office`, a US and a
  UK run in one group become one series. No heuristic can fix this; it needs
  better metadata in the playlist.
- **A film whose group does not contain one of the VOD keywords becomes a live
  channel**, and so does a series grouped under a word like `Documentaries`. The
  word list is English-only, so a `Películas` or `Séries` group falls through
  too. The startup summary's channel count and VOD-group list are how you spot
  it.
- **Prefixless titles with no usable `tvg-name` fall back to the group name**,
  so distinct shows in one group merge. The summary's `from group name` count
  measures how often this happened.
- **A prefixless title whose `tvg-name` holds only an episode title splits one
  series into one entry per episode.** `1x01` with `tvg-name="Pilot"` has no
  season/episode marker in `tvg-name` to divide on, so the whole of it — the
  episode's own name — becomes the series name, and the next episode's differs.
  This is the narrow case where identity is genuinely absent from both fields
  rather than merely ambiguous: tier 2 cannot tell an episode title from a
  series title. Such a playlist reads as many one-episode series, which the
  summary shows as a `from tvg-name` count close to the VOD row count.

Widening the keyword list or letting configuration declare the mapping would
help the second and third, but both are the owner's decision, not the adapter's
— the log and this section are the visibility the current design offers.

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
| `ADOBOFLIX_FILE_PATH` | conditional | Path to a playlist already on disk (`.json`, `.m3u` or `.m3u8`). Used when nothing has been imported. With neither, opening the mode fails naming this key. |
| `ADOBOFLIX_PLAYLIST_FILE` | no | Where a playlist imported through the UI is stored (default `.adoboflix/playlist`; the extension is added from the detected format). |

An **explicit path** — the one the server chose for an import — wins over
`ADOBOFLIX_FILE_PATH`, so a stale environment value cannot override what the
user just imported. `source.Config.FilePath` carries it, additively, exactly as
`source.Config.PlaylistCode` carries a credential; no adapter gained a write
method.

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

**What this change adds.** `/api/v1/source/playlist-code` is a new
unauthenticated endpoint that **accepts a credential and writes it to disk**.
That is consistent with the posture above — the server binds `127.0.0.1` by
default and `/api/v1/proxy` is unauthenticated too — but it is a step up in
consequence. The proxy leaks a CDN host to whoever can reach the port; this
endpoint accepts and stores the subscriber's key. Anyone who can reach the port
can replace the stored code (a denial of service against the subscriber) and,
because a submitted code is validated against AdoboTV, can use the server as an
oracle to test codes. If AdoboFlix ever becomes multi-user or
internet-exposed, **these endpoints need authentication before anything else
does** — before the proxy, before resolve.

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
