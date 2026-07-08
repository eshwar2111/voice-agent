# SP2 — Ambient Deep Context + Wake Word (Design Spec)

**Date:** 2026-07-08
**Status:** Approved (design), pending implementation plan
**Builds on:** SP1 (tiered resolver spine). Two features shipped together in one plan.

---

## Program context

Second slice of the low-overhead tiered-intelligence assistant. SP1 gave us the Tier-0 local
resolver + Tier-1 cloud fallback, wired into both the voice and text paths, plus an opt-in
voice build (`-tags whisper`). SP2 adds:

- **Feature A — Ambient deep context:** the assistant automatically understands what you're
  looking at (selection, clipboard, active app, and the screen when needed) on the invocations
  you *already* use — no new hotkey to learn.
- **Feature B — Wake word:** hands-free activation via the dormant Porcupine engine, wired into
  the existing voice pipeline with a clean microphone hand-off.

Compute strategy is unchanged: cloud-LLM-only (BYOK). Context enrichment only happens on the
Tier-1 (LLM) path — Tier-0 stays instant and token-free.

---

## Feature A — Ambient deep context

### Goal

When you invoke the assistant (voice or the existing Ctrl+Space command bar), it automatically
captures your current desktop context and the LLM uses it, so requests like "summarize this",
"reply to this", or "explain this error" just work — **with zero new hotkeys or modes.**

### Success criteria

1. A Tier-1 request issued right after selecting text / copying / viewing an app receives that
   context automatically — no extra user step.
2. **Tier-0 latency does not regress**: local commands ("open notepad") still execute instantly;
   the expensive parts of context capture and the LLM call only happen on the Tier-1 path.
3. Selection capture is **non-destructive** — it never leaves the user's clipboard changed.
4. A screenshot is captured **only** when the request implies visual context or there is no
   usable text — never on every command.

### The capture-timing constraint (the crux)

Selection must be captured **while the target app still has focus**, i.e. at the *instant of
invocation*, before any assistant UI appears:

- **Voice** (pill / wake word): the overlay uses `WS_EX_NOACTIVATE` and never steals focus, so
  the underlying app keeps its selection. Capture happens when the trigger fires (and can overlap
  the multi-second recording — no latency cost).
- **Typed** (Ctrl+Space): the command bar takes focus once shown, so selection is captured in the
  hotkey handler **at the keypress, before the bar is shown**.

### Units

- **`internal/context/capture.go`** — the ambient grabber.
  ```go
  type Capture struct {
      AppName     string // foreground process name
      WindowTitle string
      Clipboard   string // truncated
      Selection   string // "" if none; non-destructively grabbed
      Screenshot  []byte // nil unless visual context was requested
  }
  // CaptureAmbient grabs the cheap signals (window, clipboard) instantly and, when
  // grabSelection is true, the current selection non-destructively (save clipboard →
  // Ctrl+C with a short settle → read → restore clipboard).
  func CaptureAmbient(grabSelection bool) Capture
  // WithScreenshot returns a copy with Screenshot populated (captured lazily on the Tier-1
  // visual sub-path).
  func (c Capture) WithScreenshot() Capture
  // String renders the text context block for the LLM system prompt.
  func (c Capture) String() string
  ```
  `CaptureAmbient` supersedes the ad-hoc context building in `command.RunAICommand` and the old
  `agentctx.BuildContext`; those callers migrate to it.

- **`internal/context/visual.go`** — `func NeedsScreenshot(instruction string) bool`: true when
  the instruction contains visual cues ("screen", "this error", "what am i looking at", "see
  this", "on my display", …). Pure, table-tested.

### Data flow / integration

Capture is threaded into dispatch, replacing the bare `activeApp string` param:

```
invocation (voice trigger OR Ctrl+Space keypress)
   └─ CaptureAmbient(grabSelection=true)   // at invocation, before UI focus shift
        │  (voice: overlaps recording; typed: before bar shown)
        ▼
   input (transcript or typed text)
        ▼
   dispatch.Handle(ctx, input, cap)        // cap replaces activeApp
        ├─ Tier 0 (resolver match): execute immediately — cap IGNORED (stays instant)
        └─ Tier 1 (miss/low-conf):
             ├─ text path:   Orchestrator.Run(ctx, input, cap.String())   // sysContext added
             └─ visual path: if NeedsScreenshot(input) || cap has no text →
                                cap.WithScreenshot(); Provider.Generate(prompt, [screenshot])
                                → answer shown in output overlay + optional TTS
```

Signature changes:
- `dispatch.Deps.Handle(ctx, input string, cap contextpkg.Capture) error` (was `activeApp string`).
- `agent.Orchestrator.Run(ctx, userText, sysContext string)` — adds `sysContext`, injected into
  the decomposition prompt and forwarded to sub-agents. Existing callers pass the captured context
  (or "" where none).

**Tier-0 protection:** `Handle` runs the resolver *first*. On a Tier-0 match it never touches
`cap`'s expensive fields, so the selection settle / screenshot cost is only paid on Tier-1. For
typed input, `CaptureAmbient` is invoked in the hotkey handler with a **short** Ctrl+C settle
(target ≤ ~80 ms, tunable) so opening the bar stays snappy; for voice the grab overlaps recording.

### Error handling

- No selection / empty clipboard / screenshot failure → those fields are simply empty; the LLM
  gets whatever context is available. Never blocks the command.
- Clipboard restore is best-effort; if it fails, log and continue (do not error the user's command).

---

## Feature B — Wake word

### Goal

Hands-free activation: say "Porcupine" and the assistant starts listening for your command —
no click, no key. Wires the dormant `internal/wakeword` Porcupine engine into the existing voice
pipeline.

### Success criteria

1. With voice enabled and a Porcupine access key set, saying the wake word triggers the same
   capture→transcribe→dispatch flow as clicking the pill.
2. The wake-word listener and the command recorder **never contend for the microphone**.
3. After a command completes, wake-word listening **resumes automatically**.
4. Disabled cleanly when voice is off or no access key — zero background mic use.

### Units

- **`internal/wakeword/porcupine.go`** — refactor the one-shot `ListenForWakeWord` into a loop:
  ```go
  // StartWakeWordLoop listens for the built-in "Porcupine" keyword until ctx is cancelled.
  // On each detection it stops its own recorder (releasing the mic), calls onDetect and BLOCKS
  // until onDetect returns (the command finished), then restarts listening.
  func StartWakeWordLoop(ctx context.Context, accessKey string, onDetect func()) error
  ```
  Porcupine + pvrecorder are initialized once; the recorder is `Stop()`/`Start()` around each
  `onDetect` so the command pipeline (malgo) owns the mic while a command runs.

### The microphone hand-off (the crux)

Porcupine captures via `pvrecorder`; the command pipeline records via `malgo`. They cannot hold
the mic simultaneously. The loop enforces a strict baton:

```
loop:
  recorder.Read() → Process(pcm)
  on keywordIndex >= 0:
     recorder.Stop()                 // release mic
     onDetect()                      // fires ui.ListenTrigger; BLOCKS until command done
     recorder.Start()                // reclaim mic, resume
```

`onDetect` must block until the command's capture+dispatch has fully finished. It signals the
engine (via `ui.ListenTrigger`) and waits on a completion signal (the engine's busy state going
idle, exposed as a done channel / callback). The plan defines the exact completion signal.

### Wiring, build tag, config

- Wake word is voice-only, so it compiles under the **same `whisper` (voice) build tag** as ASR:
  `internal/wakeword` gets `//go:build whisper` plus a `//go:build !whisper` stub
  (`StartWakeWordLoop` returns immediately in the stub build). This keeps the default lean build
  free of the Porcupine native libs.
- Started from `main.go` **only** when `cfg.EnableVoice && cfg.PorcupineAccessKey != ""`, in a
  goroutine bound to the root context.
- Keyword: built-in **"Porcupine"** for v1. A custom phrase (a `.ppn` file) is a later add.

### Risks

- **Porcupine/pvrecorder native libs** may need the same toolchain treatment as whisper (matching
  GCC / DLL present on `PATH`). The plan surfaces and handles it; worst case documents the
  requirement like `docs/BUILD-VOICE.md`.
- Mic contention if `onDetect` returns before the recorder actually stops recording — mitigated by
  the strict Stop→onDetect(block)→Start baton and by the engine's existing `isBusy` guard.

---

## Non-goals (SP2)

- No new dedicated hotkey (ambient context replaces the originally-considered hotkey).
- No custom wake-word phrase (built-in "Porcupine" only for now).
- No proactive/ambient *triggers* (that's SP3).
- No change to Tier-0 resolution or the compute strategy.

---

## Files touched (anticipated)

**New:** `internal/context/capture.go`, `internal/context/visual.go`,
`internal/wakeword/stub.go`; tests alongside.

**Modified:** `internal/wakeword/porcupine.go` (loop + build tag), `internal/dispatch/dispatch.go`
(`Handle` takes `Capture`; Tier-1 text/visual sub-paths), `internal/agent/orchestrator.go`
(`Run` gains `sysContext`), `internal/command/router.go` (capture at Ctrl+Space; pass `Capture`),
`internal/command/hotkey.go` (capture at keypress before showing the bar),
`internal/engine/runtime.go` (capture at voice trigger; pass `Capture`), `cmd/app/main.go` (start
wake-word loop under voice tag/config), `docs/BUILD-VOICE.md` (wake-word note).

---

## Testing

- **Capture** (`capture.go`): injected clipboard/selection/window providers; assert non-destructive
  clipboard restore, empty-field tolerance, and that `grabSelection=false` skips the Ctrl+C.
- **Visual heuristic** (`visual.go`): table test of instructions → `NeedsScreenshot` true/false.
- **Dispatch**: Tier-0 match still makes zero provider calls and ignores `cap` (extends the SP1
  no-LLM-call test); Tier-1 text path forwards `cap.String()` as sysContext (fake provider records
  the received context); visual path triggers screenshot capture when `NeedsScreenshot`.
- **Wake-word loop**: fake recorder + fake detector (no real audio) drive the Stop→onDetect→Start
  baton; assert the recorder is stopped before `onDetect` runs and restarted after, and that the
  loop exits on ctx cancel.
- **Manual / headless:** synthetic wake-word detection triggers the pipeline; a TTS round-trip
  ("summarize this" with seeded selection) confirms context reaches the LLM path.
