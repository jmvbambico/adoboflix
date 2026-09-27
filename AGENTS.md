# AdoboFlix — Agent Guide

AdoboFlix is a **local, self-hosted player client**. It plays content. It owns
no content, no accounts, and no playlists.

---

## The First Law — read-only against AdoboTV

**AdoboFlix must never write to the AdoboTV database or mutate AdoboTV state.**

AdoboTV (`~/projects/adobotv-server`) is the authoritative IPTV/VOD platform.
AdoboFlix is one of its clients. Every query AdoboFlix issues against the
`adobotv` database is a `SELECT`. No `INSERT`, no `UPDATE`, no `DELETE`, no
`CREATE`, no DDL — not to `playlists`, not to `playlist_channels`, not to
`streams`, not to `channels`, not to `vod_assets`, not to anything.

This is not a style preference. AdoboTV has its own ingestion pipeline, its own
scan endpoints (`POST /v1/channels/scan`, `POST /v1/vod/scan`), and ~150 specs
of normalization rules behind them. A second writer corrupts all of it.

**If a change needs to persist something, it persists locally** — a file the
user downloads, or local browser/app state. Never upstream.

A PR that introduces a write against the AdoboTV database is rejected on sight,
regardless of test status.

---

## What AdoboFlix is for

1. An **AdoboTV subscriber** connects their account and streams their entitled
   content through a modern web player.
2. Someone with **no AdoboTV account** clones the repo, points it at their own
   playlist, and gets the same player.

Both paths feed one internal library format. The player does not care where the
content came from.

---

## Source adapters

Content reaches AdoboFlix through a **source adapter**. Adapters are
interchangeable and strictly read-only.

- **`adobotv-http`** — the real path. Talks to AdoboTV over HTTP using the
  user's playlist code. Playback flows through AdoboTV's `/v1/play/*`
  redirector so the operator's analytics stay correct.
- **`file`** — a local playlist (JSON or M3U) for users with no account.
- **`postgres-direct`** — **a development test harness only.** It taps the
  `adobotv` database directly to verify the player can resolve and play
  content (DRM, Shaka, proxying) without standing up an account. It is not a
  supported end-user path, it bypasses entitlement and analytics, and it must
  never become the default. Guard it behind explicit configuration.

New adapters implement the same read-only interface. No adapter gets a write
method.

---

## Health scanning

AdoboFlix may probe streams to tell its user what is dead. It **reports**; it
does not repair.

- Probe results go to an in-memory result set and a **downloadable report file**
  the user can send to the operator out of band.
- Never write `check_status`, never touch `last_check`, never create or rebuild
  a playlist, never delete `playlist_channels` rows.
- The authoritative scan is AdoboTV's, server-side, and is not AdoboFlix's to
  trigger.

---

## Gates

Run before every commit. These are the full gate:

```bash
go build ./cmd/server          # must compile
go vet ./...                   # must be clean
cd client && npm run lint      # tsc --noEmit, must be clean
cd client && npm run test:run  # vitest, must be green
```

Worker-scope agents run all four — they are fast, hermetic, and touch no
shared state. There is no integration suite yet; when one is added it runs only
on the integration branch.

`npm run test:run` is the non-watch form; `npm test` watches. Both need
devDependencies present, so an environment with `NODE_ENV=production` needs
`npm ci --include=dev` — `npm ci` alone omits them and the command will not
be found.

---

## Conventions

- **Go**: stdlib + Gin + sqlx. Dependencies are vendored (`go mod vendor`) —
  re-vendor when `go.mod` changes. Errors wrap with `%w`. No panics in handlers.
- **Columns are explicit.** `SELECT *` breaks against the AdoboTV schema because
  it carries columns AdoboFlix does not map (see `entryColumns` in
  `internal/db/db.go`). Add the column to the struct and the list, or do not
  select it.
- **TypeScript**: strict. No `any` in new code. The client calls only
  `/api/v1/*` on its own origin.
- **Secrets**: `.env` only, never committed. `.env.example` documents every key.
- **Do not edit** `vendor/`, `static/`, or `client/dist/` by hand — all three
  are build output.

---

## Repo map

```
cmd/server/main.go        entry point, route table
internal/db/              read-only queries against the adobotv schema
internal/handler/         HTTP handlers
internal/epg/             XMLTV decode from compiled_epg (gzipped blob)
internal/scanner/         stream health probing
internal/middleware/       CORS
client/src/               React 19 + Vite + Tailwind 4
client/src/components/CustomPlayer.tsx   Shaka Player — HLS, DASH, DRM
static/                   built client, served by the Go binary
```

---

## Code intelligence

This repo carries a `.codegraph/` index; `codegraph explore "<symbol or question>"`
(or the codegraph MCP tool) answers most navigation questions in one call and works
today. The GitNexus section below is auto-generated between its markers and is
regenerated by `npx gitnexus analyze` — leave it alone when editing this file. Its
MCP server is not always reachable; when it is unavailable, use codegraph and do not
block on the GitNexus "MUST" steps.

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **adoboflix** (594 symbols, 835 relationships, 13 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> If any GitNexus tool warns the index is stale, run `npx gitnexus analyze` in terminal first.

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `gitnexus_impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `gitnexus_detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `gitnexus_query({query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `gitnexus_context({name: "symbolName"})`.

## Never Do

- NEVER edit a function, class, or method without first running `gitnexus_impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `gitnexus_rename` which understands the call graph.
- NEVER commit changes without running `gitnexus_detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/adoboflix/context` | Codebase overview, check index freshness |
| `gitnexus://repo/adoboflix/clusters` | All functional areas |
| `gitnexus://repo/adoboflix/processes` | All execution flows |
| `gitnexus://repo/adoboflix/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->