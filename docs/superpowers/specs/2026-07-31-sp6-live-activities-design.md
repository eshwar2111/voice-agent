# SP6 — Live Activities & the Split Island (Design)

Date: 2026-07-31
Status: Approved
Builds on: `docs/superpowers/specs/2026-07-29-dynamic-island-shell-design.md` (SP5)
Scope: new package `internal/island/`, plus `internal/ui/` changes.

## Problem

SP5 built the island and a live-activity registry, but the registry is a **display**
mechanism, not a data one:

- Activities are **ephemeral and push-only**. Something has to call `UpdateActivity` from tool
  code; nothing produces activities on its own.
- **Only one is ever visible.** The registry sorts by priority and renders the top entry; a
  running timer is invisible the moment music starts.
- There is **no lifecycle**. An activity has no defined beginning, duration, or end beyond a
  TTL, and nothing owns keeping it current.

The result reads as a status line that occasionally changes, not as a Dynamic Island.

## Goal

Make the island genuinely *live*: activities that start, persist while they are true, update
themselves from real data, and end — with **two visible at once** via a split island.

## Non-goals

Desktop widgets (explicitly dropped — the island replaces that idea; see Decisions).
Activity persistence across app restart. Multi-monitor. The SP7–SP9 catalog below.

---

## 1. Decisions taken during design

Recorded because each closed off an alternative:

1. **Island only; desktop widgets dropped.** A Spotify "widget" *is* the Spotify Live Activity.
   Keeping both would have forced the multi-window / union-region question immediately and
   roughly doubled the work before anything shipped.
2. **Split island, exactly two visible.** Faithful to iPhone and the most recognizable
   behavior. Three was rejected: the caps get cramped and the island starts reading as a
   toolbar rather than a single object.
3. **Shy by default, wake on change.** A running activity still shrinks to dormant, but keeps a
   minimal glyph so the glance survives the shrink. Always-full-size was rejected as a
   permanent 260px bar above the browser tabs; fully-hidden was rejected because it removes the
   glance, which is the entire value.
4. **The user can dismiss a running activity.** Dismissal is keyed on the activity's `ID` plus
   its `Started` timestamp. An update to a still-live activity does **not** clear it — otherwise
   a timer you dismissed would reappear one second later, making dismissal useless. Dismissal
   clears only when that ID is `end`ed and later re-emitted with a new `Started`, i.e. a
   genuinely new instance. Dismissing hides the activity from the island; it does **not** stop
   the underlying thing (the timer keeps running).
5. **No persistence across restart.** A 25-minute timer dies with the process. Persisting it
   means a store plus a resume protocol for a rare case.
6. **A Live Activity has a beginning, a middle, and an end.** Applied as a filter during design:
   it is why battery level, CPU/temp gauges, disk-space warnings, and VPN state were rejected.
   Anything permanently true is a status bar, not this.

---

## 2. Provider framework

New package `internal/island/`, following the pattern `internal/trust` already established:
**dependency-free, stdlib only, all coupling injected inward.** It imports nothing from
`agent`, `tools`, `ui`, or `llm`.

```go
type Activity struct {
    ID       string         // "timer.pomodoro" — stable identity
    Kind     string         // render family: "timer" | "meeting" | "job"
    Priority int
    Data     map[string]any // render slots read this
    Started  time.Time
    Ends     time.Time      // zero = open-ended

    // Significant marks an update worth interrupting the user for — it is what
    // sets `wakeUntil` and swells the island out of dormant (§3). The emitter
    // decides, not the registry: a timer's per-second tick is NOT significant,
    // but the same timer reaching zero is. Without this on the struct there is
    // no way to distinguish the two, and the island either twitches every
    // second or never wakes at all.
    Significant bool
}

type Provider interface {
    Name() string
    Run(ctx context.Context, emit func(Activity), end func(id string)) error
}
```

Each provider owns one goroutine and one concern. A `Registry` holds what is live and pushes to
the UI through an injected `Publish func([]Activity)` — so `internal/island` never imports
`internal/ui`, and the whole thing is testable with a fake publisher and a fake clock.

### Coalescing — the load-bearing rule

Providers emit at wildly different rates: a timer ticks every second, a transfer could emit at
60Hz, a meeting poll runs once a minute. Pushing every emit through the bridge into a JS
re-render is the performance bug this design must avoid.

**The registry coalesces: at most one push per activity per 250ms.** Terminal events (activity
ended; timer reached zero) **bypass the coalescer** so they are never delayed.

This single rule is what allows a progress-heavy provider to be added in SP9 without redesigning
anything.

### Lifecycle

`Run` returns when the activity is over. Context cancellation stops all providers on shutdown.
A panicking provider is recovered and logged rather than taking down the agent.

---

## 3. The split island

### Window shape — already supported

`region.go` (SP5) builds the window shape by unioning multiple rounded rects via
`CombineRgn(RGN_OR)`; it was written for "island + panel + toast". **A detached bubble is just
another rect in that list.** No new Win32 work: JS publishes two shapes instead of one.

### Geometry

- Bubble: 32px circle, 8px to the right of the main pill; 44px at `peek`.
- **The pair stays centered, not the pill.** When a bubble appears the main pill shifts left by
  half the bubble's total width so the assembly remains screen-centered. Without this, every
  activity that starts visibly shoves the island sideways — which reads as a bug even when
  deliberate.

### Slot assignment

Priority order, reusing SP5's resolver: highest priority owns the main pill, second owns the
bubble, the rest stay live but unrendered. An approval arriving during music therefore takes the
pill and demotes music to the bubble — correct, because the approval is blocking a plan and the
music is not. **Clicking the bubble promotes it**, swapping the two.

### Motion

The bubble enters with a spring scale from 0 with slight overshoot and exits by scaling back to
0 — the same asymmetric principle as SP5, since an arriving object should feel physical and a
departing one should not linger. The pill's shift-left uses the same curve and duration so the
two read as one coordinated movement.

### Wake-on-change

`resolve()` gains a `wakeUntil` timestamp. A provider marking an update **significant** — track
changed, timer hit zero, call connected — sets it, and `resolve()` returns `peek` while
`now < wakeUntil` (~2.5s), then falls back.

This keeps `resolve()` pure and keeps it the **sole geometry authority**, the invariant the
entire SP5 architecture rests on. Routine updates (a timer's per-second tick) do **not** set it,
so the island does not twitch every second.

### Auto-shy changes meaning

Today `dormant` is an empty 168x32 capsule. With an activity running it keeps a minimal glyph —
album art, a countdown ring — at dormant size, and the bubble stays visible. The glance must
survive the shrink, or shy-by-default becomes hidden-by-default.

---

## 4. The three v1 activities

Chosen because **none requires a new integration**, so nothing external can block the platform.

| Activity | Ingestion | Compact | Expanded | Significant (wakes) |
|---|---|---|---|---|
| `timer.pomodoro` | **polled provider** — existing timer tool | `12:04` + draining ring | label, Pause, Cancel | start, zero |
| `meeting.next` | **polled provider** — Google Calendar, 60s | `Standup in 5m` | join link, attendees | T-5m, T-1m, start |
| `agent.job` | **push** — existing `SetState`/`ShowNotification` | `3 of 5 · Opening Chrome` | goal + step list | start, each step, done |

`agent.job` **absorbs** SP5's `agent.run` rather than sitting beside it, but it stays
**push-driven**: it continues to be fed by the ~30 existing `SetState`/`ShowNotification` call
sites, whose signatures do not change. What it gains from the new registry is lifecycle and
persistence — it is now eligible for the bubble slot, so a long research job stays visible while
music owns the pill.

No executor-observing provider is introduced. Inventing one would mean a second, parallel source
of truth for agent progress alongside the 30 call sites that already work.

---

## 5. Coexistence with SP5

The registry supports **two ingestion paths, one output** — not two parallel systems:

- **Polled providers** (new): timer, meeting.
- **Push-driven** (SP5, unchanged): `agent.job`, `trust.approval`, and `ambient.nudge` are
  push-driven — nothing polls for "the user needs to approve this". `UpdateActivity` /
  `EndActivity` keep their signatures and behavior.

`trust.approval` keeps **no TTL and fail-closed** resolution. That is a safety property, not a
preference: it must never auto-deny a plan the user is still reading, never silently approve,
and never deadlock the executor.

`spotify.nowplaying` stays as-is until SP7's SMTC provider replaces it.

---

## 6. Error handling

- **Panicking provider** — recovered, logged, that provider stops; others keep running. One bad
  feed must never take down the island or the agent.
- **Provider errors** — retry with backoff. A Calendar outage must not permanently kill meeting
  countdowns.
- **Terminal events bypass the coalescer.** A timer hitting zero is the update that matters most
  and must never be rate-limited.
- **Registry cap: 8 live activities.** A buggy provider emitting unique IDs in a loop cannot grow
  the list unbounded.
- **Injected clock**, so threshold and countdown logic is testable without sleeping.

---

## 7. Testing

`internal/island` is pure Go — no Win32, no WebView — so it is genuinely unit-testable:

- priority ordering and two-slot assignment
- coalescing, **including the terminal-event bypass**
- dismissal semantics (dismissed stays dismissed until a new instance, not the next update)
- provider panic recovery and error backoff
- the 8-activity cap
- all with a fake clock and fake publisher

JS side: `resolve()` gains `wakeUntil` and two-slot assignment; **pair-centering math becomes a
pure function in `geometry.js`** alongside `unionIslandRect` — the shape that made SP5's region
math testable after it caused a defect.

Motion quality remains manual QA. It always will.

---

## 8. Sequence after this spec

Grouped by **kind of risk**, so each project resolves one class of unknown:

- **SP6 (this spec)** — platform + split island + timer, meeting, job. Zero new integrations.
- **SP7 — Presence & media.** SMTC system-wide now-playing, mic/camera in-use + global mute,
  call detection, recording indicator. One WinRT/COM spike gates all four.
- **SP8 — Agent-native.** Smart clipboard actions, AI email triage, live meeting assistant.
  **The meeting assistant means continuously recording meeting audio** — it needs an explicit
  consent decision and probably a hard local-only guarantee, raised as a first-class question in
  that spec rather than buried in a task.
- **SP9 — External feeds.** Sports, stocks, package tracking, file-transfer progress. All need
  third-party APIs and user-supplied keys, and all must degrade gracefully offline.

Known carried-forward issues, unchanged by this spec: 175%/200% DPI still break the fixed
window; webview_go's own `WM_DPICHANGED` handler resizes the window independently of
`canvas.Attach()`.
