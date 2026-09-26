# Source adapters

AdoboFlix plays content. It does not own content. Everything it plays arrives
through a **source adapter**, and every adapter is strictly read-only.

This document is the contract. It was derived by reading AdoboTV's own
handlers, and every claim below was checked against the live schema or the
live `settings` table — the surprises are marked.

---

## The three adapters

| Adapter | For | Status |
|---|---|---|
| `adobotv-http` | A subscriber with an AdoboTV account | The real path |
| `file` | Someone with no account and their own playlist | Supported |
| `postgres-direct` | Verifying the player itself | **Development harness only** |

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

### ⚠ `channels[].url` is a decoy — do not play it

It is the `channel_url_placeholder` setting, whose **live value is
`https://www.youtube.com/watch?v=dQw4w9WgXcQ`**. An adapter that naively
reads `url` will rickroll every viewer.

The playable path is **always `runtime_attr_url`**.

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

The adapter must detect the splash response and report **"device pending
approval"**. It must not render it as an empty library.

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

## Why not the database

`postgres-direct` is a test harness, and the reason is not hygiene.

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
