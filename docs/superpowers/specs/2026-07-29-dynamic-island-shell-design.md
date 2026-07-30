# SP5 — Dynamic Island Shell (Design)

Date: 2026-07-29
Status: Approved
Scope: `internal/ui/` only. No changes to callers.

## Problem

The overlay is a single WebView window that is **physically resized** for every view
(`VIEW={pill:[300,52], command:[720,440], output:[720,540], confirm:[680,410],
suggestion:[400,150], settings:[1160,760]}` in `overlay_v2.html`). Each transition is a
`SetWindowPos` + `SetWindowRgn` + forced `RedrawWindow` (`overlay.go:217-252`).

Consequences:

- No transition can be animated. A morph cannot cross the window boundary, so content is
  clipped mid-animation. The `RedrawWindow(0x0585)` call exists purely to erase the "grey
  ghost" WebView2 leaves behind when the window shrinks.
- Shape and content are conflated. `updateUI()` sets text *and* resizes, requiring the
  defensive "snap back to pill" guard at `overlay_v2.html:279` to stop the pill rendering as
  a giant ellipse.
- Go→JS is string interpolation into JavaScript source
  (`w.Eval(fmt.Sprintf("updateUI('%s');", sk))`). This has caused at least one shipped bug
  (`d4e5cdd`, "approval card showed `[object Object]`") and one defensive workaround
  (`overlay.go:176`).
- `overlay_v2.html` is 324 lines with all CSS and JS inline. It cannot absorb an island
  state machine plus a widget host.
- Agent progress surfaces as an opaque `Working…` string, and trust-layer approvals take
  over the entire panel.

## Goal

Replace the resize-driven overlay with a **Dynamic Island**: a single continuous object that
morphs fluidly between sizes, reacts to hover, and swells on its own when the agent or an
integration has something to say.

This spec covers the **shell only**. The widget platform is a separate, sequenced spec that
builds on the surface defined here.

## Non-goals

Widget platform; multi-monitor; exclusive-fullscreen (game) behavior; real DWM acrylic within
the window region; island drag/dock. All are follow-on work.

**Widget placement note:** the fixed 1200x800 window cannot host widgets scattered across the
desktop. SP6 must choose between a region that is a union of disjoint shapes across a larger
window, or one window per widget. This spec deliberately does not decide that.

---

## 1. Window model — region-shaped

> **Revised 2026-07-29 after the Task 0 spike FAILED.** The original design used a
> full-screen transparent canvas with `WS_EX_TRANSPARENT` click-through. On this machine the
> flag did not make a WebView2-hosted window click-through — clicks were swallowed in every
> mode tested. Switching frameworks would not have helped: Electron's `setIgnoreMouseEvents`,
> Tauri's `set_ignore_cursor_events`, and Wails' `SetIgnoreMouseEvents` are all wrappers over
> the same Win32 flag. The design below needs no such flag.

**One WebView window at a fixed 1200x800**, transparent, created
`WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE`, and **never resized after creation**.
1200x800 is the smallest box that contains the largest surface (Control Center, 1160x760).

The window may be *moved* (`SetWindowPos` with `SWP_NOSIZE`) to re-anchor a surface — moving
does not trigger a WebView2 relayout. Only resizing does, and resizing never happens.

**`SetWindowRgn` clips the window to the shape of whatever is currently visible.** Pixels
outside the region are not part of the window, so clicks land on the desktop *by definition*.
There is no flag that can fail and no cursor loop to run.

All morphing still happens in CSS inside the fixed window, so springs may overshoot, caps may
stretch, and content may cross-fade freely.

Deleted as a direct result: `resizeWindow()`, the `VIEW` size map, `callResize`, and the
`RedrawWindow` ghost-buster. They exist to compensate for *resize* artifacts, and the window
no longer resizes. `SetWindowRgn` and `CreateRoundRectRgn` are retained and repurposed.

### Region sync — two calls per transition, not sixty

The obvious implementation animates the region in lockstep with the CSS, which needs either a
JS->Go message per frame or the same easing curve duplicated on both sides (and then a clock
skew problem). Neither is necessary.

During a morph the region is set **once, to the bounding box of the from-shape and the
to-shape**. It is then set once more, to the exact target shape, when the morph settles:

- `region:set` on morph start -> `union(from, to)`, so the region is never smaller than what
  CSS is drawing at any point in the transition. Nothing can be clipped mid-animation.
- `region:set` on morph end -> the exact target rect and radius.

The cost is that for the ~460ms of a grow, the region is larger than the visible island, and
that surplus is transparent window that eats clicks. It is bounded, brief, and confined to the
area the island is about to occupy anyway.

The region is inflated by 2 CSS px before conversion to physical pixels, to absorb DPI
rounding. Multiple visible surfaces (island + nudge card) combine with `CombineRgn(RGN_OR)`.

### Consequence for hit-testing

`internal/ui/hittest.go`'s `hit()` function — built in Task 2 under the old design — becomes
dead code and is removed. `Rect` and `rectRegistry` survive: JS still publishes the rects of
visible surfaces, but Go now builds a *region* from them instead of testing a cursor against
them. The registry's empty-fallback safety property carries over unchanged, and matters more:
an empty region would make the whole window vanish.

### Known trade-off: shadows

The region is a hard crop, so any CSS `box-shadow` extending past the island's border box is
clipped away. The island therefore relies on its 1px stroke and inner highlight for edge
definition, plus whatever shadow DWM draws for the region.

If that reads as too flat in the Task 8 QA pass, the fallback is to inflate the region by the
shadow radius (~20px) and accept a transparent, click-eating halo of that width around the
island. That is a QA-time decision, not a design-time one.

### Honest note on "glass"

`backdrop-filter` over a transparent WebView2 page blurs nothing — today's frosted glass is a
translucent dark fill. It looks good and is retained as-is. A region-shaped window *can* carry
real DWM acrylic within its region, which is the one genuine upside of this approach over the
canvas design, but it stays a non-goal for v1.

---

## 2. Asset layout

`w.SetHtml(htmlTemplate)` becomes a `go:embed`'d directory served over a loopback listener on
a random port, with `w.Navigate`. This buys real ES modules and separate files, and is the
same mechanism the widget platform will use to load widgets.

```
internal/ui/
  overlay.go          window lifecycle + bindings (loses ~120 lines of resize code)
  canvas.go           NEW — fixed-size window creation, DPI, topmost, extended styles
  region.go           NEW — rects -> CreateRoundRectRgn/CombineRgn -> SetWindowRgn
  hittest.go          rect registry (hit() removed; see §1)
  bridge.go           NEW — typed Go->JS event push
  assets.go           NEW — go:embed FS + loopback server
  assets/
    index.html
    css/{tokens,island,surfaces,controlcenter}.css
    js/{main,motion,activities,region,state}.js
    js/surfaces/{command,result,approve,controlcenter}.js
```

`overlay_v2.html`'s visual language (tokens, glass, panel styles) carries over decomposed, not
rewritten. `overlay.html` (1615 lines, dead) is deleted.

---

## 3. Island: state, geometry, motion

### Two orthogonal axes

- **presence** — geometry only: `dormant -> compact -> peek -> expanded -> sheet`
- **content** — what is rendered, resolved from agent state + active activity + open surface

A single pure `resolve(store) -> {presence, contentId}` runs after every input. The morph
engine diffs against current and animates. **Nothing else may set size.** The current bug
class (stray `updateUI('idle')` snapping the window; the goroutine workaround at
`overlay.go:293`) becomes structurally inexpressible.

### Geometry (CSS px)

| presence | size | radius | when |
|---|---|---|---|
| dormant  | 168x32, 50% opacity | 16 | idle 6s+, cursor away |
| compact  | 260x40  | 20 | idle, awake |
| peek     | 420x52  | 26 | hover — quick actions + activity title |
| expanded | 560x180 | 28 | activity expanded, inline approval |
| sheet    | 720x520 | 30 | command input, results |

Control Center is **not** on this ladder. It is a separate surface, screen-centered, that
fades in while the island stays put.

### Motion

1. **Shape springs, different curve per direction.**
   Grow: `460ms cubic-bezier(.22,1.16,.36,1)` (~4% overshoot, settles).
   Shrink: `380ms cubic-bezier(.36,0,.24,1)` (no overshoot — a bouncing retreat reads as
   unstable). Radius animates alongside width/height so the shape is always a true capsule.

2. **Content lags the shape.** Outgoing: `120ms` to `opacity 0, scale(.96), blur(4px)`.
   Incoming: `90ms` delay, then `200ms` to `opacity 1, scale(1)`. The container reaches its
   new size before new content lands. Island interior is `overflow:hidden` with
   absolutely-positioned content so text never reflows mid-morph. This is the detail that
   separates a morphing object from a box that resizes.

3. **Caps translate, never cross-fade.** Leading/trailing icon slots persist across state
   changes and slide to their new positions, creating object permanence.

The OS `prefers-reduced-motion` setting collapses all of the above to instant cuts. No
`config.json` field is added — that would require changing `internal/config`, and section 6
commits to touching nothing outside `internal/ui/`. A user-facing toggle can follow later.

### Auto-shy

After 6s idle with the cursor away, the island drops to `dormant` (168x32, 50% opacity) so it
does not fight with browser tab strips. It wakes to `compact` on hover, on any activity, or
when a surface opens.

---

## 4. Live activities

Each activity registers four render slots:

```js
registerActivity({
  id: 'spotify.nowplaying',
  priority: 20,
  leading(d),    // left cap  — art / icon / risk color
  trailing(d),   // right cap — waveform / stop / countdown
  compact(d),    // middle line, shown at peek
  expanded(d),   // full body, shown at expanded
  ttl, onDismiss
})
```

Go pushes `activity:update {id, data}` and `activity:end {id}`. The registry sorts by
priority; the highest owns the caps.

Resolution order: **open surface (user intent) > approval > agent run > nudge > now-playing >
idle.** User action always outranks anything the agent wants to say.

This is deliberately the same shape widgets will take: a widget is a registration with a
placement instead of a priority.

### v1 activities

| id | pri | leading | trailing | middle |
|---|---|---|---|---|
| `trust.approval`      | 100 | risk-colored shield | — | inline Approve / Cancel, auto-expands, no ttl |
| `agent.run`           | 90  | pulsing mic -> progress ring | live waveform -> stop | streaming transcript -> `3 of 5 - Opening Chrome` |
| `ambient.nudge`       | 50  | source icon | one-tap action | nudge text, 8s ttl |
| `spotify.nowplaying`  | 20  | album art | waveform | track - artist; peeks 4s on change, then recedes to caps |

`agent.run` replaces the opaque `Working…` string. `trust.approval` replaces the full-panel
`RequestConfirmationCard` takeover.

### Icons

Inline SVG sprite in the embedded assets — 20px, 1.75 stroke, `currentColor`. No icon font,
no CDN; the page must work fully offline. Replaces `&#9881;`, `&times;`, and the `⭳ ▦ ↗ △`
glyph map at `overlay_v2.html:298`. Set: mic, waveform, spotify, mail, calendar, download,
link, shield, gear, stop, chevron, sparkle, search, folder, terminal.

---

## 5. Go <-> JS bridge

### Go -> JS

Exactly one eval shape:

```go
func (b *Bridge) Push(kind string, payload any)   // -> __agent.recv({"kind":…,"data":…})
```

The whole envelope is marshalled as one JSON value. No user string ever becomes JavaScript
source again; the `[object Object]` bug class is closed structurally rather than by
remembering to quote.

Events: `state`, `activity:update`, `activity:end`, `surface:open`, `surface:close`, `config`.

**Ready-gating.** `overlay.go:367`'s `time.Sleep(250ms)` before grabbing the HWND is a race
dressed as a delay. JS calls a `uiReady()` binding on load; the Bridge buffers pushes until
then and flushes in order, and window styling happens on that callback. The sleep is removed.

### JS -> Go

- Kept: `triggerListen`, `submitCommand`, `getPrevCommand`, `getNextCommand`,
  `confirmCallback`, `suggestionAccept`, `suggestionDismiss`, `setInputActive`, `quitApp`,
  `jslog`, `getSettings`, `saveSettings`, and all nine OAuth bindings.
- Deleted: `callResize`.
- Added: `uiReady()`, `setRegionRects(rects)`, `islandAction(id)`.

---

## 6. Migration — public API is unchanged

30 call sites across 12 files use 8 exported functions. **All signatures are preserved and
reimplemented on top of the island. No file outside `internal/ui/` changes.**

| Go API | becomes | callers |
|---|---|---|
| `SetState` | `state` event -> drives `agent.run` | `engine/runtime.go` (6), `tools/speak.go` (2) |
| `ShowNotification` | `agent.run` narration line (peek-level, no size jump) | `agent/orchestrator.go` (3), `tools/research_tool.go` (3), `tools/spotify_ai.go` (2), `tools/spotify_workflow_agent.go` (3), `main.go` |
| `ShowOutputOverlay` | `surface:open{result}` -> sheet | `agent/executor.go`, `dispatch/dispatch.go`, `tools/productivity.go`, `tools/speak.go` |
| `RequestConfirmationCard` / `RequestConfirmation` | `trust.approval` activity, inline expanded; still blocks on `confirmChan` | `agent/executor.go` (2), `security/confirmation.go`, `ui/ask_step.go`, `main.go` |
| `ShowSuggestion` / `SetMeetingAlert` | `ambient.nudge` activity | `main.go` (via `ambient.DelivererFunc`) |
| `ShowCommandBarInOverlay` | `surface:open{command}` -> sheet | `ui/command_bar.go` |

`ShowNotification` is a **narration channel**, not a notification channel — `orchestrator.go:69`
sends `Step 2/5: …`, `research_tool.go:92` sends `Reading: …`. Mapping it to the `agent.run`
middle line yields the step ticker from 30 existing call sites without touching any of them.

---

## 7. Error handling

- **Approval fails closed.** `RequestConfirmationCard` blocks on `confirmChan` while
  `executor.go:108`/`:183` hold a plan open. **`trust.approval` has no TTL** — it must not
  auto-deny a plan the user is still reading. It resolves `false` only on explicit cancel,
  island dismissal, or app quit. Never silently approves; never deadlocks the executor.
- **Empty region is fatal, so it cannot happen.** With a region-shaped window, a region of
  zero area makes the entire UI invisible — a worse failure than the old design's. If JS never
  publishes rects, `rectRegistry.Get()` returns the fallback rect covering the island's maximum
  bounds, and `region.go` additionally refuses to apply any region whose total area is zero,
  leaving the previous region in place and logging. The window can never disappear.
- **Asset server.** `net.Listen("127.0.0.1:0")`, random port, serves only the embedded FS under
  a per-launch random path prefix. Bind failure logs and shows a message box rather than
  opening a window that renders nothing.
- **Unknown activity id** -> dropped with a `jslog` line; never throws inside the render loop.

---

## 8. Testing

Win32 code is not unit-testable, so logic is deliberately pushed out of it.

- **`region.go`** splits into a pure `regionShapes(rects []Rect, radius, inflate float64,
  scale float64) []physRect` that converts published CSS rects into the physical-pixel
  rounded-rects `CreateRoundRectRgn` will consume. Table-driven Go tests: DPI 1.0 / 1.25 / 1.5,
  the 2px inflation, empty-registry fallback, zero-area rejection, and `union(from, to)`
  bounding-box math for the morph-start case.
- **`hittest.go`** keeps `rectRegistry` and its empty-fallback tests. `hit()` and
  `TestHit` are deleted with the click-through design.
- **`bridge.go`** — envelope marshalling against hostile payloads: quotes, newlines,
  `</script>`, emoji, lone surrogates. Direct regression test for `d4e5cdd`.
- **`assets/js/state.js`** holds `resolve()` and activity priority sorting as pure,
  dependency-free functions with a `node --test` file. Optional, **not** wired into the Go
  build — Node is not a dependency of this project and will not become one.
- **Manual QA checklist** (in the implementation plan): clicks pass through over browser tabs;
  hover latency; every morph transition; 100/125/150% DPI; reduced-motion; approval
  fail-closed; no focus theft while typing in another app.

---

## 9. Sequencing

1. ~~Spike `WS_EX_TRANSPARENT` click-through.~~ **Done — FAILED.** Design revised to
   region-shaped windows (§1); no spike gates the remaining work.
2. **Done.** `assets.go` + loopback server + `go:embed`; `overlay_v2.html` ported verbatim.
3. **Done (partially superseded).** `hittest.go` + tests. `Rect`/`rectRegistry` carry forward;
   `hit()` is removed in step 4.
4. `canvas.go` fixed 1200x800 transparent window + `region.go` (`SetWindowRgn` from published
   rects) + tests; remove `hit()`.
5. `bridge.go` + `uiReady` gating + tests; convert all `w.Eval` call sites.
6. Island geometry, `resolve()`, motion engine, `region:set` on morph start/settle; icon sprite.
7. Activity registry + the four v1 activities.
8. Rehome surfaces (command, result, approve, Control Center); delete `resizeWindow`, `VIEW`,
   `callResize`, `overlay.html`, `overlay_v2.html`.
9. Manual QA pass, including the shadow-clipping decision from §1.

Widget platform follows in its own spec.
