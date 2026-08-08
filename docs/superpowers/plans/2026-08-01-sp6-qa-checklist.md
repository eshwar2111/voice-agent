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
| 11 | Meeting provider backoff on outage | With a meeting active/pending, disconnect network access (disable Wi-Fi/adapter) and wait about 2 minutes, then reconnect. | No crash. No activity gets stuck in a broken state. `voice-agent.log` shows retry log lines from the `meeting` provider with escalating backoff (not one line every few seconds indefinitely). Reconnecting lets it recover and resume normal polling. | |
| 12 | Registry cap at 8 | Start more than 8 concurrent timers (or otherwise trigger 8+ simultaneous provider-driven activities) in quick succession. | The island shows at most 8 live activities at once (`island.MaxLive`). `voice-agent.log` records a line for each drop past the cap. The UI stays responsive — no freeze, no runaway animation. | |
| 13 | Log hygiene | After exercising checks 1–12, review the full `voice-agent.log` for this session. | No `unhandled event`, no `unknown activity`, no JS console errors surfaced in the log. | |

---

## Outcome

- [ ] All 13 checks pass → branch is mergeable
- [ ] Any failures logged below with repro steps

### Failures found

_(record here)_
