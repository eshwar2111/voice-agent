# SP5 Dynamic Island — Manual QA Checklist

Branch: `feat/sp5-dynamic-island` · 27 commits · 34 JS tests + 13 Go tests passing

**Why this exists.** Every agent in this build ran without a desktop. Every visual claim in every
task report is explicitly marked "deferred to controller QA" — none of them observed the island
move. Everything verified so far is structural: tests, builds, code paths, invariants checked by
grep. This list is the part that needs eyes.

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

Fill in Result / Notes as you go.

---

## A. The checks that gate merge

These exercise paths that no test could reach, ranked by what the final reviews flagged as
highest risk.

| # | Check | Pass criteria | Result |
|---|---|---|---|
| A1 | **Type a multi-word command, pause 5s mid-sentence, keep typing** | Text survives. This is the direct acceptance test for the bug that made typing impossible (rebuilt the textarea once per second). | |
| A2 | **Move the mouse in and out of the command sheet while typing** | Text survives. Same bug, different trigger. | |
| A3 | **Click-through** — click a browser tab in the row beside the island | The tab activates; the overlay does not eat the click. *This is the check the entire architecture pivot exists for.* | |
| A4 | **Watch the island edge during a grow** (idle → command sheet) | No hard rectangular crop that snaps clean ~400ms in. The region should widen before the shape does. | |
| A5 | **Watch a shrink** (sheet → compact) | Same — no clipping of the still-painted island. | |
| A6 | **Two long results back to back** — get a >55-char answer, then let a timer complete while the sheet is open | The second result replaces the first, and **Copy copies the new text**. Regression fixed late; highest-value repro. | |
| A7 | **Run a multi-step command** (`ai give me my Google Workspace brief`) and watch the pill continuously | Shows a step ticker (`3 of 5 · …`), never goes blank mid-operation, never shows `Working…` as the only feedback. | |
| A8 | **Approval end-to-end**: trigger a gated action, then (separately) click Approve, click Cancel, click the island body while the prompt is up, press Esc, and quit the app with a prompt open | Each resolves cleanly; the executor never hangs. No Go test can exercise `confirmChan` against a real WebView. | |
| A9 | **Double-click Approve rapidly** | Only one approval registers. A stray second click must be dropped and logged, never delivered to a queued prompt. Check `voice-agent.log` for the drop line. | |
| A10 | **Two approvals at once** — start a voice command needing approval, then submit `ai <something risky>` from the command bar | Both prompts appear in turn; neither goroutine hangs. | |

## B. Display and scaling

| # | Check | Pass criteria | Result |
|---|---|---|---|
| B1 | **100% DPI** | Island geometry correct; region aligns with painted edges. | |
| B2 | **125% DPI** (restart after changing) | Same. This is where the 2px region inflation earns its keep. | |
| B3 | **150% DPI on 1080p** — open Control Center, click **Close** and **Quit** | Both reachable. The window height is clamped here; this was broken until the final fix wave. | |
| B4 | **Taskbar overlap at 150%** | The dashboard's bottom edge sits over the taskbar strip — confirm the overlay wins z-order and the controls are still clickable. | |

> **Known broken, out of scope:** 175% and 200% DPI. At 200% the CSS viewport is 960×540, so the
> 1160×700 dashboard clips both ways and Close/Quit go off-screen again. Deferred to SP6.

## C. Motion quality — the subjective part

No test can answer these. They are the actual request.

| # | Check | What to judge | Result |
|---|---|---|---|
| C1 | **Grow overshoot** | Does it read as physical, or bouncy and cheap? Curve is `460ms cubic-bezier(.22,1.16,.36,1)`, ~4% overshoot. | |
| C2 | **Shrink settle** | Should NOT bounce — a bouncing retreat reads as unstable. `380ms cubic-bezier(.36,0,.24,1)`. | |
| C3 | **Content lag** | The shape should reach its new size *before* the new content lands (90ms delay). Does it feel like one object morphing, or a box resizing with a cross-fade bolted on? | |
| C4 | **Cap permanence** | With Spotify playing, hover the island: album art should **slide** to its new position, not blink out and back. | |
| C5 | **Shadow / depth** | The region is a hard crop, so CSS shadows past the island's border box are clipped. Does it read flat? **If yes**, the spec has a documented fallback: inflate the region by ~20px and accept a transparent click-eating halo. This is a judgment call left deliberately for someone who can see it. | |
| C6 | **Auto-shy** | After 6s idle with the cursor away, the island shrinks to 168×32 at 50% opacity. Too subtle? Too aggressive? | |

## D. Behavior and hygiene

| # | Check | Pass criteria | Result |
|---|---|---|---|
| D1 | **Reduced motion** — enable Windows "Show animations off" | All transitions become instant cuts; no broken layout; region still correct. Takes effect mid-session (no restart). | |
| D2 | **Settings reachable in every state** | Gear works while idle, while Spotify plays, while an agent runs, and while an approval is open. This bug was fixed twice. | |
| D3 | **No focus theft** | Type continuously in Notepad while the agent runs a plan — no keystroke lost. | |
| D4 | **Toast visible** | `Copy` on a result and `Save` in settings both show a visible toast. It was clipped out of the region until the final fix wave. | |
| D5 | **Dormant wake** | Let the island go dormant, then confirm it wakes on hover, on a Spotify track change, and on an incoming approval. | |
| D6 | **Startup** | Launch 10×. Island always appears; no blank canvas, no dropped first event, no flash of a default-styled window. | |
| D7 | **Log hygiene** | `voice-agent.log` has no `unhandled event`, no `unknown activity`, no JS errors. | |
| D8 | **Control Center round-trip** | All four tabs, Save, and one OAuth connect. Esc closes. | |

---

## Outcome

- [ ] All A-series pass → branch is mergeable
- [ ] C5 shadow decision recorded: **keep hard crop** / **inflate region ~20px**
- [ ] Any failures logged below with repro steps

### Failures found

_(record here)_
