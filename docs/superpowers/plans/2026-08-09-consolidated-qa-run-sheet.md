# Consolidated QA Run Sheet — 2026-08-09

Everything that needs a human at the desktop, in the order that minimises setup thrash.
Report results as `A3 pass` / `B7 fail` + screenshot. Rows already settled without a desktop are
**not** repeated here — see the SP5/SP6 checklists for those.

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -tags whisper -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

`-tags whisper` is not optional. Without it voice silently fails: the island still shows
"Listening…", and the real error only appears in `voice-agent.log`.

**Screenshots:** only where the pass criteria is visual. Rows marked 📋 are log-based — paste the
relevant `voice-agent.log` lines instead, a screenshot won't show it.

---

## Block 0 — Preflight (do these first, they block later rows)

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| 0.1 📋 | Google link actually took | Restart the app. Watch the log for 60s. | No `invalid_grant`, no `provider meeting has failed`. If it still fails, the re-link didn't stick and **E1–E2 are blocked**. |
| 0.2 📋 | Porcupine | Check the log at startup. | If `ACTIVATION_REFUSED` — wake word is dead, **B5 is blocked**. Either renew the key or tell me to drop wake-word from scope. |

---

## Part A — Regression tests for this session's fixes

Nothing below has ever run on a real machine. These are the highest-value rows on the sheet.

### A. Voice capture

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| A1 | **Delayed speech** — the headline fix | Click the pill. Wait a deliberate **3 seconds in silence**, then say "open notepad". | Notepad opens. Before the fix the recording ended during your pause and this failed with `audio too short`. |
| A2 | **Normal speech still ends promptly** | Click the pill, say "what time is it", then stop. | Recording stops ~2s after you finish — it does not sit there for the full 10s. |
| A3 📋 | **No-speech bailout** | Click the pill and say nothing at all. | Log shows `🤐 No speech detected` within ~4s, and the island returns to idle. It must not hold the mic for 10s. |
| A4 | **Mid-sentence pause** | Click the pill, say "open", pause ~1.5s, say "notepad". | Captured as one utterance. This is the boundary case — tell me if it clips. |

### B. Double-fire (this session's fix)

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| B1 📋 | **Rapid double-click the pill** | Click the pill twice as fast as you can. | Exactly **one** capture starts. Log shows `island click ignored — within trigger cooldown`. No `Already processing a command` spam. |
| B2 📋 | **Click during a running command** | Start a slow command (`ai write me a paragraph about go concurrency`), then click the pill 3-4 times while it runs. | Log shows `island click ignored — agent is <state>` for each. No second capture, no repeated execution. |
| B3 | **Back-to-back commands stay responsive** | Run a command, let it finish, immediately click the pill again. | Second capture starts at once — the cooldown must not make you wait. |
| B4 📋 | **Speak-then-click** | While the agent is speaking a response, click the pill. | Log: `TTS is active — ignoring trigger`. No feedback loop, no capture of its own voice. |
| B5 📋 | **Wake word + click race** *(blocked if 0.2 fails)* | Say the wake word, then immediately click the pill. | One capture. The wake-word loop must not reclaim the mic mid-command. |

### C. Network resilience (this session's fix)

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| C1 📋 | **The wedge** — the fix that matters most | Start a cloud-bound command (`what's the weather like in tokyo`), and **disable Wi-Fi within 1s of triggering it**. | Within ~60s the island returns to **idle** with an error. Then re-enable Wi-Fi and run any command — **it works**. Before the fix the island stuck on Executing permanently and every later trigger was ignored until restart. |
| C2 | **Streaming isn't truncated** | With a good connection, run a command that streams a long answer (`ai explain how tcp congestion control works in detail`). | The full answer arrives. If it cuts off mid-sentence around 60–90s, the header timeout is wrong — tell me. |

### D. Tools (this session's fixes)

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| D1 | **`create_file` won't clobber** | Create a file with content: `create a file called qa-test.txt with the text hello world`. Check it. Then run the **same command again**. | First: file appears **on your Desktop** with `hello world` in it — not empty, not next to the .exe. Second: refuses with "already exists", and **the original content survives**. Before the fix it silently wiped the file and reported success. |
| D2 | **`open_file` by name** | `open my qa-test file` | Opens the file created in D1. No "missing file_path" error, no wrong file. |
| D3 | **`open_explorer` by name** | `open the voice agent folder` | Opens `E:\Voice Agent`. Not "no path given", not the wrong folder. |
| D4 | **Search phrasing** | `search for the weather in delhi` | Results are about the weather in Delhi — **not** a search for the literal string "for the weather in delhi". |
| D5 | **"pause" isn't stolen** | `pause for a second, what's on my calendar` | Answers about your calendar. It must **not** pause your music and discard the question. |
| D6 | **Bare pause still works** | Play music, then say `pause`. | Music pauses. |
| D7 | **Gmail by query** *(needs 0.1)* | `read my latest email` | Returns an actual email. Before the fix this required a message ID nothing could supply. |
| D8 | **Sheets by name** *(needs 0.1)* | `add a row with test data to my <some sheet name> spreadsheet` | Finds the sheet by name and writes. Approve the confirmation when prompted. |
| D9 | **Browser doesn't hang** | `open example.com in the browser and read the page` | Completes. If a page ever stalls, the plan fails after ~45s rather than freezing forever. |
| D10 | **TTS doesn't peg a core** | Ask something with a long spoken answer. Watch Task Manager → voice-agent.exe CPU **while it speaks**. | CPU stays low (single digits). Before the fix a busy-wait pinned a full core for the whole utterance. |

---

## Part B — Island QA (the unrun SP5/SP6 rows)

### E. Setup-free: idle island

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| E1 | **Click-through** ⭐ | With the island idle, click a browser tab in the row *beside* it. | The tab activates. The overlay does not eat the click. *This is what the whole architecture pivot exists for.* |
| E2 | **Grow edge** | Press Ctrl+Space (idle → command sheet). Watch the island's edge. | No hard rectangular crop snapping clean ~400ms in. The region widens before the shape does. |
| E3 | **Shrink settle** | Esc to close the sheet. | No clipping of the still-painted island. Should **not** bounce — a bouncing retreat reads as unstable. |
| E4 | **Grow overshoot** | Watch E2 again. | Judge: physical, or bouncy and cheap? (~4% overshoot.) |
| E5 | **Content lag** | Watch E2 again. | The shape should reach its new size *before* the content lands (90ms). One object morphing, or a box resizing with a cross-fade bolted on? |
| E6 | **Auto-shy** | Leave it idle 6s, cursor away. | Shrinks to 168×32 at 50% opacity. Judge: too subtle, too aggressive, or right? |
| E7 | **Dormant wake** | From dormant, hover it. | Wakes. |
| E8 | **Shadow / depth** | Look at the island against a light background. | The region is a hard crop, so shadows past the border box are clipped. **Does it read flat?** If yes there's a documented fallback (inflate the region ~20px, accept a transparent click-eating halo) — this is a judgment call that needs your eyes. |
| E9 | **Startup ×10** | Launch and quit 10 times. | Island always appears. No blank canvas, no dropped first event, no flash of a default-styled window. |

### F. Command sheet

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| F1 | **Type, pause 5s, keep typing** | Type a multi-word command, stop 5s, resume. | Text survives. Direct acceptance test for the bug that rebuilt the textarea once per second. |
| F2 | **Mouse in/out while typing** | Same, moving the cursor in and out of the sheet. | Text survives. |
| F3 | **Click the textarea** | Ctrl+Space, then click into the text field. | Sheet stays open. (Regressed once already.) |
| F4 | **No focus theft** | Type continuously in Notepad while the agent runs a plan. | No keystroke lost. |

### G. Results and toasts

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| G1 | **Two long results back to back** | Get a >55-char answer, then let a timer complete while the sheet is open. | The second replaces the first, and **Copy copies the new text**. Highest-value repro on this sheet. |
| G2 | **Toast visible** | Click Copy on a result; Save in Control Center. | Both show a visible, unclipped toast. |

### H. Approvals

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| H1 | **Approval end-to-end** | Trigger a gated action, then separately: click Approve, click Cancel, click the island body while the prompt is up, press Esc, and quit the app with a prompt open. | Each resolves cleanly. The executor never hangs. |
| H2 📋 | **Double-click Approve** | Rapidly double-click Approve. | One approval registers. Log shows the stray click dropped. |
| H3 | **Two approvals at once** | Start a voice command needing approval, then submit `ai <something risky>` from the command bar. | Both prompts appear in turn. Neither goroutine hangs. |

### I. Live activities — needs a timer running

Set one with `set a timer for 2 minutes`.

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| I1 | **Countdown ring** | Watch the full 2 minutes. | Ring drains smoothly. Only the fill moves — no twitching, no re-animating the ring shape each second. |
| I2 | **Timer-zero wake** | Let it reach 0:00. | Island wakes immediately — no ~250ms coalescing delay. |
| I3 | **Dormant glyph survives** | With a timer running, leave the cursor away 10s. | Island shrinks, but the ring glyph is **still visible**. Glance-value survives the shrink. |
| I4 | **Dismiss keeps the timer** | Dismiss the activity from the island. | It disappears from the island, but the timer **still fires** at its original time. |
| I5 | **Cap at 8** | Start 10+ timers quickly. | At most 8 shown. UI stays responsive — no freeze, no runaway animation. |

### J. Split — needs a timer **and** Spotify playing

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| J1 | **Split appears** | Timer running, then start music. | Island splits: one activity owns the pill, the other becomes a bubble beside it. |
| J2 | **Split stays centered** | Watch the exact moment the bubble arrives. | The pill+bubble **pair** stays centered as a unit. The island must not jump sideways. |
| J3 | **Bubble promotion** | Click the bubble. | The two swap. |
| J4 | **Bubble hit-region is tight** | Click the desktop just beside the bubble, close to its visible edge but outside it. | The click reaches the desktop. The click-eating region must not be oversized relative to what's drawn. |
| J5 | **Cap permanence** | With Spotify playing, hover the island. | Album art **slides** to its new position — it must not blink out and back. |
| J6 | **Approval interrupts music** | With music holding the pill, trigger a gated action. | Approval takes the pill, music demotes to the bubble. Dismissing it still **fails closed** — the action does not proceed without explicit Approve. |
| J7 | **Click-through DURING a morph** ⭐ | Trigger a morph (start music with a timer up, or click the bubble to swap). **While the ~460ms animation is visibly still moving**, click the desktop just outside the island's current footprint, near where the shape is heading. | The click reaches the desktop. This is the region-clipping bug that recurred **three separate times** in this project. It's now centrally guarded by `morphInFlight`, and this row is the only place that guard is ever exercised by a human. |
| J8 | **Toast during a morph** | Trigger a morph and, while it's animating, cause a toast (Copy on a result, or Save in Control Center). | Toast fully visible, not clipped. Island shape not cut off or glitched by the toast's region publish landing mid-morph. |

### K. Meeting — needs Google linked (0.1) + an event within the hour

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| K1 | **Meeting countdown + thresholds** | Watch a meeting approach (or fast-forward with a test event). | Countdown appears at T-60m. Island peeks at **T-5m** and **T-1m**, each firing **once** — no repeat wake at the same threshold on later polls. |
| K2 📋 | **Backoff recovery** | With a meeting pending, disable Wi-Fi ~2 min, then reconnect. | Escalating backoff in the log (already confirmed), and — the untested half — **reconnecting resumes normal polling**. |

### L. DPI and display

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| L1 | **100% DPI** | Set, restart. | Geometry correct; region aligns with painted edges. |
| L2 | **125% DPI** | Set, restart. | Same. This is where the 2px region inflation earns its keep. |
| L3 | **150% DPI on 1080p** | Set, restart, open Control Center. | **Close and Quit both reachable.** Broken until the final fix wave. |
| L4 | **Taskbar overlap at 150%** | Same session. | Dashboard's bottom edge sits over the taskbar — overlay wins z-order, controls still clickable. |

> Known broken, out of scope: 175% and 200%. At 200% the CSS viewport is 960×540, so the
> 1160×700 dashboard clips both ways.

### M. Behaviour and hygiene — run last

| # | Check | Steps | Pass criteria |
|---|---|---|---|
| M1 | **Reduced motion** | Enable Windows "Show animations off". | All transitions become instant cuts. No broken layout, region still correct. Takes effect mid-session — no restart. |
| M2 | **Settings reachable in every state** | Gear while idle, while Spotify plays, while an agent runs, while an approval is open. | Works in all four. This bug was fixed **twice**. |
| M3 | **Control Center round-trip** | All four tabs, Save, one OAuth connect, Esc to close. | Clean. |
| M4 📋 | **Final log hygiene** | After all of the above, review the whole session log. | No `unhandled event`, no `unknown activity`, no JS errors. |

---

## Priority if you're short on time

Run these nine first — each covers something that has either broken repeatedly or has never been
observed at all:

**C1** (the network wedge) · **B1** (double-fire) · **A1** (delayed speech) · **D1** (create_file
clobber) · **E1** (click-through) · **J7** (click-through during a morph) · **G1** (result
replacement) · **L3** (150% DPI reachability) · **H1** (approval end-to-end)

---

## Results

_(paste pass/fail here as you go)_
