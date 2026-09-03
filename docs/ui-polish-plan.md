# Dynamic Island — UX/Design Audit & Polish Plan

_From a read-only UX audit of `internal/ui/assets` (HTML/CSS/JS), `internal/ui/*.go`, `internal/island/*.go`, and the SP5/SP6 specs. Feeds the "make it cooler / more Apple" phase after existing features are verified._

## Verdict
**The motion + geometry architecture is genuinely Apple-grade** — asymmetric springs, content-lags-shape, translating caps, a single pure `resolve()` geometry authority, unit-tested region math. Keep all of it. What drags it toward "vibecoded" is the **surface** (fake glass, clipped shadows, hand-drawn icons, thin palette), the **emptiness** (idle/dormant/peek say little), and **advertised capabilities wired in JS but never fed by Go**.

## Built-but-unfed (these overlap with "make existing work", do FIRST)
- **`spotify.nowplaying` is dead code** — the JS activity (album art, equalizer, track/artist) exists but no Go calls `UpdateActivity("spotify.nowplaying", …)`. The marquee music-in-the-island + split story never appears. QA Block J depends on this. **Highest leverage.**
- **`agent.run` never becomes visual** — spec wanted a progress ring / waveform / "3 of 5 · Opening Chrome"; reality is a static glyph + a chevron that only hides the display (can't cancel). Step count only shows if the orchestrator happens to put "Step 2/5" in the narration string.
- **Timers can't be paused/cancelled from the island** — only a dismiss chevron that (by design) does NOT stop the timer.
- Also unfed/dead: legacy `approve.js` sheet, unused `showConfirmCard`.

## UX / productivity gaps
- **G4** Idle ("Ready" + mic + gear) and dormant (bare capsule) waste prime real estate — no at-a-glance clock/next-meeting/status.
- **G5** Peek offers exactly one action (gear); hover just widens.
- **G6** Now-playing (once fed) has no transport/scrubber though the tools exist.
- **G7** Meeting expanded is thin (Join only; no attendees/countdown/snooze).
- **G8** Command-sheet suggestions are 4 hardcoded chips — no palette, no recent/app-aware suggestions; discoverability ~0.
- **G9** Result rendering handles 3 JSON types then flat `\n`+`**bold**` — no code blocks/lists/links/tables.
- **G10** No unified notification/history timeline.

## Cool / Apple-grade gaps (visual + motion)
- **V1** Fake glass — near-opaque dark fills, no real translucency/blur over the desktop (biggest "not Apple" tell).
- **V2** Clipped shadows → flatness (region hard-crops any shadow past the border box; QA E8).
- **V3** Hand-drawn icons (homemade SVG sprite; crude Spotify/mic glyphs) vs SF-Symbols quality.
- **V4** Thin single-accent palette (one blue does everything; no per-activity color identity).
- **V5** Dark-only; transparent body; no light-desktop handling.
- **V6** Typography competent but flat; `--ink/-2/-3` tokens underused in activities.
- **V7** Only the countdown ring is "delightful" — no numeric roll, art breathing, button spring, wake glow.
- **V8** "Ready" is a placeholder, not a designed idle state.

## Prioritized plan  (S ≈ hours · M ≈ 1–2 days · L ≈ 3+ days)

### P0 — highest leverage, low risk
| # | Change | Why | Effort | Files |
|---|---|---|---|---|
| P0-1 | **Wire `spotify.nowplaying`** — a provider (poll or push from the Spotify tool layer) emitting track/artist/art/isPlaying to `UpdateActivity`. | Turns on the flagship dead activity + the whole split story. | M | new `internal/island/nowplaying.go` / hook `internal/tools/spotify_*.go` → `activities.js:295` |
| P0-2 | **`agent.run` real progress ring + structured steps** — reuse `ringSVG`; parse `Step x/y` into `{step,total,label}`. | The most-seen activity becomes an activity, not a status line. | M | `activities.js:248`, `overlay.go` SetState/ShowNotification shape, `orchestrator.go` |
| P0-3 | **Real depth without un-clipping** — inset shadows / inner vignette + crisp top highlight (or the E8 region-inflate fallback). | Kills the flatness tell with CSS. | S–M | `tokens.css:28`, `island.css`, maybe `region.go` |
| P0-4 | **SF-Symbols-grade icon set** (consistent weight, real media/Spotify glyphs). | Removes the most obvious homemade signal everywhere. | S–M | `index.html` sprite |
| P0-5 | **Design idle + dormant** — subtle clock / "Ask anything"; dormant keeps a live glance (next-meeting dot / breathing mic). | Reclaims wasted real estate; island always says something useful. | S | `main.js:278`, `island.css` |

### P1 — high value, moderate effort
| # | Change | Why | Effort |
|---|---|---|---|
| P1-1 | Timer **Pause/Cancel** controls wired to the timer store | spec'd control; countdown you can act on | M |
| P1-2 | Now-playing **transport + scrubber** (play/pause/next/prev/seek) | drive music from the island | M |
| P1-3 | **Per-activity color identity** (`--activity-accent`: Spotify green, meeting amber, timer teal, agent blue) | Apple's quiet color richness | S |
| P1-4 | **Peek quick-actions per activity** (mic-tap idle; dismiss + primary action) | hover does something | M |
| P1-5 | **Richer result rendering** (code blocks + copy, lists, links, tables) | common LLM output stops looking plain | M |
| P1-6 | **Context-aware command suggestions** (recent, connected-app-aware, time-of-day) | discoverability + speed | M |
| P1-7 | **Micro-interaction pass** (numeric roll, art breath, button spring, wake glow) | the delight layer | M |

### P2 — polish / stretch
| # | Change | Effort |
|---|---|---|
| P2-1 | **Real DWM acrylic within the region** (`SetWindowCompositionAttribute`) — the definitive material fix | L |
| P2-2 | Light-mode / theme adaptation (or deliberate always-dark w/ light-desktop-aware border) | M |
| P2-3 | Meeting enrichment (attendees, ring, running-late/snooze) | M |
| P2-4 | Accessibility pass (aria roles/labels, focus rings, live-region announcements) | S–M |
| P2-5 | 175/200% DPI correctness | L |

## New live-activity / surface ideas (ranked)
1. **System-wide now-playing scrubber (Windows SMTC)** — any media (browser/Spotify/local) with art, draggable scrubber, transport. Reuses the `spotify.nowplaying` slots. *Most "Jarvis-meets-Apple".*
2. **Command palette (⌘K)** — fuzzy filter over tools/apps/recent with icons + keyboard nav; turns the sheet into a real launcher.
3. **Meeting join countdown, first-class** — live ring, big Join at T-1m, mute-until-end, attendee avatars (provider exists).
4. **Download / long-job progress ring** — generic `job.progress` activity in the bubble slot; reuses `ringSVG` + split machinery.
5. **Clipboard smart-action glance** — tracking number/address/code/phone → one-tap action; fits `ambient.nudge`.
6. **Ambient status glance** — designed idle/dormant face (clock, next-event dot, connection health).

## Cross-cutting rules for implementation
- **Never set a size directly** — everything flows store → `resolve()` → render loop. New activities register slots in `activities.js`/`kindRenderers`; new providers go in `internal/island/` behind injected `Publish`/`emit`.
- **Respect the region hard-crop** for all shadow/material work (paint inside the box, or inflate the region deliberately).
- **Extend the motion engine, don't rebuild it** — the delight gap is content-level micro-interactions.
- **Wiring producers is often higher-leverage than new UI** (see built-but-unfed).
