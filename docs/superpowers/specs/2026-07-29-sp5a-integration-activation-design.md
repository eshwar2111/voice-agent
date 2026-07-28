# SP5a — Integration Activation & Fixes (Design Spec)

**Date:** 2026-07-29
**Status:** Approved (design), pending implementation plan
**Builds on:** the existing tool registry (`internal/tools`), OAuth infra (`internal/auth`), SP4 trust layer.

---

## Program context

First slice of **SP5 — Integrations & Modes**. A gap review of the existing integrations found
a lot of built-but-unusable capability. SP5a is the **activation & reliability** slice: make the
integrations that already exist actually work, so the agent feels usable before we deepen them
(slice C) or add new ones like sports (slice D).

**In scope (this slice):**
1. **Web search that returns results** — replace the no-op `web_search`, DRY the DuckDuckGo
   scraper it and `research` should share.
2. **Spotify reliability + additions** — auto-recover the "no active device" 404 (transfer
   playback), device targeting, seek, like/save (adds a scope → one re-login), and harden the
   panic-prone search parsing.

**Explicitly out of scope:**
- **Microsoft** activation — deferred (user is Google-first; the 7 built tools stay unregistered).
- **Real search API** (Brave/SerpAPI) — that's slice B; here we do the no-key scraper fix.
- **Google write depth** (Gmail drafts/reply, Calendar edit, Drive up/download) — slice C.
- **Sports/weather/news** — slice D.

---

## Feature 1 — Web search returns real results

### Current state
- `internal/tools/web_search.go` — `web_search` shell-opens the browser to a DuckDuckGo URL and
  returns the literal string `"Web search initiated"`. The agent gets **nothing** to reason over.
- `internal/tools/research_tool.go` — `research` is the real path: scrapes
  `https://html.duckduckgo.com/html/`, extracts result links with a brittle regex, fetches up to
  3 pages, strips HTML, and LLM-synthesizes an answer. The scraper logic is private to this file.

### Design
- **Extract a shared helper** into a new file `internal/tools/websearch_core.go`:
  ```go
  type SearchResult struct { Title, URL, Snippet string }
  // ddgSearch runs one DuckDuckGo HTML query and returns up to max parsed results.
  // Never panics; on markup drift it returns whatever it could parse (possibly empty), nil error
  // unless the HTTP request itself failed.
  func ddgSearch(ctx context.Context, query string, max int) ([]SearchResult, error)
  ```
  Parsing captures **title, URL, and snippet** (the current regex only grabs links). Use tolerant
  extraction (find result blocks, then pull the `result__a` href/text and `result__snippet` text
  within each) and skip any block that doesn't parse instead of failing the whole call.
- **Repurpose `web_search`** (`web_search.go`): it now calls `ddgSearch(ctx, query, 6)` and returns
  the top results formatted as text the LLM can use:
  ```
  1. <Title> — <URL>
     <Snippet>
  2. ...
  ```
  No page fetch, no LLM call → fast. Drop the browser-open behaviour (a user who wants a browser
  tab uses `open_website`). Update the tool `Description()` to say it returns result snippets.
- **`research` keeps its behaviour** (fetch top pages + LLM-synthesize) but its internal
  link-finding is replaced by a call to `ddgSearch` — one scraper to maintain (DRY).

### Non-goals
- No real search API key (slice B). Still scraper-based; accepted for this slice.
- No JS rendering / ranking.

---

## Feature 2 — Spotify reliability + additions

### Current state
`internal/tools/spotify_tools.go` (+ `spotify_ai.go`, `spotify_assistant.go`,
`spotify_workflow_agent.go`), auth in `internal/auth/spotify_provider.go`. 16 tools registered at
`registry.go:160-175`. Known problems (from the gap review):
- `spotify_play`/pause/etc. return `404 NO_ACTIVE_DEVICE` when Spotify has no active device, with
  no recovery — playback commands silently fail.
- No **seek**, no **like/save** (the `user-library-modify` scope isn't even requested).
- `spotify_search` parses raw JSON with `.(map[string]interface{})` chains that **panic** on a
  missing/null field.

### 2.1 Device auto-recovery + transfer
Tools obtain their client with `client, err := auth.GetSpotifyClient(ctx, t.Cfg)` and call the
existing `spotifyGet/Put/Post(ctx, client, path, ...)` helpers. The new helpers follow that shape.
- New minimal device type + helpers in `spotify_tools.go`:
  ```go
  // SpotifyDevice is the subset of GET /me/player/devices we act on.
  type SpotifyDevice struct {
      ID       string `json:"id"`
      Name     string `json:"name"`
      IsActive bool   `json:"is_active"`
  }
  // ensureActiveDevice makes sure playback has a target. If a device is already active it does
  // nothing. Otherwise it picks one (pickDevice) and transfers playback to it
  // (PUT /me/player {device_ids:[id], play:false}). Returns the active device name, or an error
  // if the user has zero devices.
  func ensureActiveDevice(ctx context.Context, client *http.Client, preferName string) (string, error)
  ```
- **Device selection is a pure function** (unit-tested), split out:
  ```go
  // pickDevice returns the id+name of the device to activate: an already-active device wins; else
  // a case-insensitive name match on preferName; else the first device. ("","") if list is empty.
  func pickDevice(devices []SpotifyDevice, preferName string) (id, name string)
  ```
- **Wrap playback tools** (`play`, `pause`, `next`, `previous`, `volume`, `seek`): on a response
  that indicates no active device (HTTP 404 whose body contains `NO_ACTIVE_DEVICE`, i.e. Spotify's
  `error.reason == "NO_ACTIVE_DEVICE"`), call `ensureActiveDevice(ctx, client, "")` once and
  **retry the original request once**. If still failing, return a clear "No Spotify device
  available — open Spotify on a device first" message. (The `spotifyGet/Put/Post` helpers currently
  swallow status; add an error path that carries the 404 body so callers can detect the reason —
  or add a small `spotifyReq` variant that returns the status code.)
- **New tool `spotify_transfer`** — params `{device: "<name>"}`; gets a client, calls
  `ensureActiveDevice(ctx, client, device)`; supports "play on my laptop/phone".
  `RequiresConfirmation()=false`.

### 2.2 Seek
- **New tool `spotify_seek`** — params `{position: string}` accepting:
  - `"1:30"` (mm:ss) or `"90"` (seconds) or a raw ms int,
  - relative `"+30s"` / `"-15s"` (reads current progress via now-playing to compute absolute).
  - Calls `PUT /me/player/seek?position_ms=<n>`.
- **Position parsing is pure** (unit-tested):
  ```go
  // parseSeekPosition converts a user string to an absolute position in ms. currentMs is used
  // only for relative "+/-" inputs. Returns an error on unparseable input.
  func parseSeekPosition(s string, currentMs int) (int, error)
  ```
- No new scope (uses `user-modify-playback-state`, already granted).

### 2.3 Like / save track
- **New tool `spotify_save_track`** — params `{action: "save"|"remove"|"check", track_id?: string}`.
  Defaults `track_id` to the **currently-playing** track when omitted ("like this song").
  - `save` → `PUT /me/tracks?ids=<id>`; `remove` → `DELETE /me/tracks?ids=<id>`;
    `check` → `GET /me/tracks/contains?ids=<id>` (returns saved/not-saved).
  - Requires a new `spotifyDelete(ctx, client, path)` helper (only `Get/Put/Post` exist today).
- **Scope change (forces one re-auth):** add `user-library-modify` and `user-library-read` to the
  scope list in `spotify_provider.go`. Existing tokens lack these, so the first call returns
  `403` insufficient scope — the tool **detects a 403 and returns a friendly message**: *"Re-link
  Spotify to enable saving songs (⚙ → Spotify)."* After the user re-links, it works.

### 2.4 Harden search parsing
- Add safe accessors in `spotify_tools.go` (used by `spotify_search` and anywhere raw maps are
  walked):
  ```go
  func str(m map[string]any, key string) string          // "" if absent/not a string
  func nested(m map[string]any, keys ...string) map[string]any // nil if any hop missing
  func firstImageURL(images any) string                  // "" if shape unexpected
  ```
  Replace the panic-prone `.(map[string]interface{})` chains in the search result formatting with
  these. No behaviour change on well-formed data; missing fields yield `""`/skipped instead of a
  panic.

### 2.5 Registration + NL routing
- Register `spotify_seek`, `spotify_save_track`, `spotify_transfer` in the `cfg != nil` Spotify
  block of `registry.go` (alongside the existing 16).
- Add them to `spotify_assistant`'s tool awareness / routing prompt so "like this song", "jump to
  1:30 / skip ahead 30 seconds", and "play on my <device>" resolve through the assistant.

---

## Architecture / boundaries

- `websearch_core.go` — new, owns `SearchResult` + `ddgSearch`. `web_search.go` and
  `research_tool.go` both consume it. No new external deps (uses `net/http` + stdlib regexp as
  today).
- Spotify additions live in `spotify_tools.go` next to the existing playback tools (they share the
  `spotifyGet/Put/Post` helpers and device/track shapes). Pure helpers (`pickDevice`,
  `parseSeekPosition`, the safe accessors) are the testable seams.
- No change to the trust layer, dispatch, or UI. New tools inherit `RequiresConfirmation()=false`
  (playback/search are safe; the trust gate still classifies `spotify_*` writes as Risky and will
  preview multi-step plans containing them).

---

## Testing

Pure units (HTTP/OAuth glue is verified manually):
- **`ddgSearch` parsing** — table test against a saved DuckDuckGo HTML fixture
  (`internal/tools/testdata/ddg_sample.html`): asserts N results with non-empty Title/URL and at
  least some Snippets; and an empty/garbage HTML input yields `[]` with no panic.
- **`parseSeekPosition`** — table: `"1:30"→90000`, `"90"→90000`, `"90000"` (raw ms) → 90000,
  `"+30s"` with current 60000 → 90000, `"-15s"` with current 20000 → 5000 (floor at 0), bad input
  → error.
- **`pickDevice`** — table: active device wins; name match (case-insensitive) beats first; no
  match falls to first; empty list → `""`.
- **safe accessors** — `str`/`nested`/`firstImageURL` on missing/null/wrong-type fields return
  `""`/nil without panicking; happy path returns the value.
- **Manual:** `web_search "..."` returns a readable result list; `research` still synthesizes;
  Spotify play with no active device auto-recovers; `spotify_transfer` to a named device;
  `spotify_seek "1:30"`; like/unlike current track (after re-link); search a query whose result
  has a null owner/album (no panic).

---

## Files touched (anticipated)

**New:** `internal/tools/websearch_core.go`, `internal/tools/spotify_seek.go`,
`internal/tools/spotify_save.go`, `internal/tools/spotify_transfer.go`, and tests alongside
(`websearch_core_test.go`, `spotify_helpers_test.go`), plus `internal/tools/testdata/ddg_sample.html`.

**Modified:** `internal/tools/web_search.go` (return results via `ddgSearch`),
`internal/tools/research_tool.go` (use `ddgSearch`), `internal/tools/spotify_tools.go`
(`ensureActiveDevice`/`pickDevice`, 404 retry wrap, safe accessors),
`internal/auth/spotify_provider.go` (add library scopes), `internal/tools/spotify_assistant.go`
(route the new tools), `internal/tools/registry.go` (register 3 new tools), `CLAUDE.md` /
`README.md` (note the activated capabilities + the one-time Spotify re-link for saving).

---

## Non-goals (SP5a)

- Microsoft activation (deferred), real search API (slice B), Google write depth (slice C),
  sports/weather/news (slice D).
- No new OAuth provider; no callback-port change.
- No change to how tokens are stored.
