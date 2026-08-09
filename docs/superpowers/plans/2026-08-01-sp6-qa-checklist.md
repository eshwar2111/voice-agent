# SP6 Live Activities — Manual QA Checklist

Branch: `feat/sp6-live-activities` (this worktree's local history; task-10 wires the registry,
providers, and dismiss binding into `cmd/app/main.go` / `internal/ui`).

**Why this exists.** No agent in this project has ever seen this UI run — every visual claim in
every task report across SP5 and SP6 is explicitly marked "deferred to controller QA." Everything
verified so far is structural: Go tests (`go test ./internal/... -race`), JS tests (`node --test`),
a clean `go build`, and `go list` confirming `internal/island` stays stdlib-only. This checklist is
the part that needs eyes on a real desktop.

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

Set a timer via voice or the command bar (`set a timer for 2 minutes`), and — for checks 10/11 —
have a Google account linked with an event on the calendar within the next hour (`Control Center →
Connections → Google`, or however OAuth linking is exposed in this build). Fill in Result as you go.

> **Run status (2026-08-09).** Same as the SP5 sheet: only rows that can be settled from code,
> tests, or existing logs are filled in below. **Checks 1–10, 12, 14, 15 remain unrun** — every
> one of them is a "watch the island move" check that needs a human at the desktop.
>
> Check 10 (meeting thresholds) is additionally *blocked*: the Google token was `invalid_grant`
> throughout the 21:20 session, so the meeting provider never produced an activity to watch.
> Re-link before attempting it.

---

| # | Check | Steps | Pass criteria | Result |
|---|---|---|---|---|
| 1 | Countdown ring | Start a 2-minute timer and watch the island for the full 2 minutes. | The island shows a countdown with a draining ring; it ticks smoothly without twitching or re-animating the ring shape each second — only the fill should move. | |
| 2 | Split on second activity | Start a timer, then start playing music (Spotify). | The island splits into two pieces: one activity owns the pill, the other appears as a bubble beside it. | |
| 3 | Split stays centered | Immediately after triggering check 2, watch the moment the bubble appears. | The pill+bubble **pair** stays centered as a unit — the island must not jump sideways when the bubble arrives. | |
| 4 | Bubble promotion | With the split from check 2 up, click the bubble. | The two swap: the previously-bubbled activity takes the pill, the previous pill demotes to the bubble. | |
| 5 | Timer-zero wake | Let a running timer count down to 0:00. | The island wakes to the peek/expanded state immediately when it hits zero — no ~250ms coalescing delay before the UI reacts. | |
| 6 | Dormant glyph survives | Leave a timer running, then leave the cursor away from the island and don't interact for 10s. | The island shrinks to its dormant/auto-shy state, but the timer's ring glyph is still visible — the glance-value survives the shrink, it isn't blanked. | |
| 7 | Dismiss keeps the timer running | With a timer showing on the island, dismiss it (swipe / dismiss control per the UI). | The activity disappears from the island immediately, but the underlying timer is unaffected — it still fires (sound/notification) at its original time. | |
| 8 | Approval interrupts music | Start music playing (so it holds the pill), then trigger an action that requires approval (e.g. an `ai` command that needs confirmation). | The approval takes the pill; music demotes to the bubble. Dismissing the approval (or letting it time out) still fails closed — the gated action does not proceed without an explicit Approve. | |
| 9 | Bubble hit-region is tight | With a split active (pill + bubble), click on the desktop just beside the bubble, close to its visible edge but outside it. | The click reaches the desktop underneath (e.g. focuses/activates whatever is there) — the bubble's click-eating region must not be oversized relative to what's drawn. | |
| 10 | Meeting countdown + wake thresholds | With a Google account linked and a calendar event starting within the next 60 minutes, watch (or fast-forward via a test event) as the meeting approaches. | A countdown activity appears once the meeting is within 60 minutes. The island wakes (peeks) at T-5m and again at T-1m, and each threshold fires only once — no repeat wake at the same threshold on subsequent polls. | |
| 11 | Meeting provider backoff on outage | With a meeting active/pending, disconnect network access (disable Wi-Fi/adapter) and wait about 2 minutes, then reconnect. | No crash. No activity gets stuck in a broken state. `voice-agent.log` shows retry log lines from the `meeting` provider with escalating backoff (not one line every few seconds indefinitely). Reconnecting lets it recover and resume normal polling. | **Half pass, unplanned repro.** The 21:20 session hit a real sustained provider outage (`invalid_grant`, not network) and behaved as specified: no crash, no stuck activity, and escalating backoff rather than a line every few seconds — `retrying in 5s` → `backing off to 10s` → `20s` → `40s`. The **recovery** half is unverified: the fault never cleared during that session, so "reconnecting resumes normal polling" still needs the real Wi-Fi test. |
| 12 | Registry cap at 8 | Start more than 8 concurrent timers (or otherwise trigger 8+ simultaneous provider-driven activities) in quick succession. | The island shows at most 8 live activities at once (`island.MaxLive`). `voice-agent.log` records a line for each drop past the cap. The UI stays responsive — no freeze, no runaway animation. | **Partial — automated only.** `registry_test.go` covers the cap and the drop path at `MaxLive = 8` (`registry.go:72`), and the drop is logged. What no test can show is the UI half: whether the island stays responsive under 8+ simultaneous activities. Still needs a human. |
| 13 | Log hygiene | After exercising checks 1–12, review the full `voice-agent.log` for this session. | No `unhandled event`, no `unknown activity`, no JS console errors surfaced in the log. | **Pass, but weak evidence.** Zero matches in the 21:20–21:21 session. That session never ran checks 1–12, which is the whole point of doing this one last — re-run it after a real pass. |
| 14 | Click-through **during** a morph, not before/after | Trigger a pill↔bubble morph (e.g. start music while a timer is showing, so the island morphs from a single pill into the pill+bubble pair — or click the bubble to trigger the promotion swap). While the ~460ms animation is **visibly still moving** (not once it has settled), click on the desktop just outside the island's current on-screen footprint, near where the shape is expanding or contracting toward. | The click reaches the desktop underneath — it must not be swallowed by a stale (too-wide or too-narrow) click-eating region left over from before the morph started. This is the region-clipping bug that recurred three separate times in this project (a new `publishRegionRects()` call site not inheriting the morph gate); it's now centrally guarded by `morphInFlight`, and this check is the only place that guard gets exercised by a human. | |
| 15 | Toast during a morph doesn't clip | Trigger a morph (as in check 14) and, while it is still animating, cause a toast to appear — e.g. click **Copy** on a result, or **Save** in Control Center, timed to land mid-animation. | The toast is fully visible, not visually clipped at the island's edge, and the island's shape is not visibly cut off or glitched by the toast's region publish landing mid-morph. | |

---

## Outcome

- [ ] All 15 checks pass → branch is mergeable
- [ ] Any failures logged below with repro steps

### Failures found

_(record here)_
