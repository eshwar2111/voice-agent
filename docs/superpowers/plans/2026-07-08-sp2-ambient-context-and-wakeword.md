# SP2 — Ambient Deep Context + Wake Word — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the assistant automatically context-aware on the invocations you already use (voice + Ctrl+Space) with no new hotkey, and wire the dormant Porcupine wake word into the voice pipeline with a clean microphone hand-off.

**Architecture:** A new `internal/context` capture unit grabs desktop context (selection/clipboard/window, screenshot-when-needed) at invocation time; it flows into `dispatch.Handle` and is used ONLY on the Tier-1 (LLM) path so Tier-0 stays instant. Wake word becomes a build-tagged background loop that hands the mic off to the command pipeline and waits for completion before resuming.

**Tech Stack:** Go 1.26, CGO (w64devkit GCC 14.1), robotgo (selection grab), atotto/clipboard, Porcupine + pvrecorder (voice tag), the SP1 `internal/dispatch` + `internal/resolver`.

## Global Constraints

- Module path: `github.com/yourname/voice-agent`. Build the C toolchain in every `go` command: `export PATH="$PATH:/c/w64devkit/bin" && go ...` (without it: `gcc: cannot execute 'as'`).
- **Do NOT run `go build ./...`, `go build ./cmd/app`, or `go test ./...`** — they link the whisper libs. Verify non-whisper packages with `go build ./internal/...`; verify `cmd/app`/`engine` compile with `go vet ./cmd/app/ ./internal/engine/` (compiles, no link). For voice-tagged code, build the voice exe explicitly with `-tags whisper` (see Task 10).
- Import the local context package aliased as `agentctx "github.com/yourname/voice-agent/internal/context"` (avoids clashing with stdlib `context`).
- **Tier-0 latency must not regress:** the resolver runs first in `dispatch.Handle`; on a Tier-0 match, the capture's expensive fields (selection settle, screenshot) are never touched.
- Selection capture is **non-destructive** (save clipboard → Ctrl+C → read → restore).
- Wake word compiles under the `whisper` build tag (the "voice" tag), with a `!whisper` stub; started only when `cfg.EnableVoice && cfg.PorcupineAccessKey != ""`.
- Explicit `git add <files>` only — never `git add -A`. Commit after every task.
- TDD where the unit is pure/testable; follow existing test style.

---

## File Structure

**New:**
- `internal/context/capture.go` — `Capture` struct, `CaptureAmbient`, `WithScreenshot`, `String`; overridable grabber vars.
- `internal/context/visual.go` — `NeedsScreenshot(instruction) bool`.
- `internal/context/capture_test.go`, `internal/context/visual_test.go`.
- `internal/wakeword/loop.go` — pure-Go, tag-free `runWakeLoop` + interfaces (testable without CGO).
- `internal/wakeword/loop_test.go`.
- `internal/wakeword/stub.go` — `//go:build !whisper` no-op `StartWakeWordLoop`.

**Modified:**
- `internal/agent/orchestrator.go` — `Run` gains `sysContext`.
- `internal/dispatch/dispatch.go` — `Handle` takes `agentctx.Capture`; Tier-1 text + visual sub-paths.
- `internal/dispatch/dispatch_test.go` — update signature; add context/visual assertions.
- `internal/command/router.go` — consume pending capture; pass to `Handle`; `RunAICommand` passes sysContext.
- `internal/command/hotkey.go` — capture at Ctrl+Space keypress before showing the bar.
- `internal/engine/runtime.go` — capture at voice trigger; carry it in the transcribed payload; `commandDone` signal.
- `internal/wakeword/porcupine.go` — `//go:build whisper`; `StartWakeWordLoop` wiring real deps into `runWakeLoop`.
- `cmd/app/main.go` — start the wake loop (voice tag/config) with an `onDetect` that triggers + waits.
- `docs/BUILD-VOICE.md` — wake-word note.

---

## Phase A — Context capture unit

### Task 1: `Capture` + `CaptureAmbient`

**Files:**
- Create: `internal/context/capture.go`, `internal/context/capture_test.go`

**Interfaces:**
- Produces:
  - `type Capture struct { AppName, WindowTitle, Clipboard, Selection string; Screenshot []byte }`
  - `func CaptureAmbient(withSelection bool) Capture`
  - `func (c Capture) String() string`
  - `func (c Capture) WithScreenshot() Capture`
  - overridable vars `grabWindow func() (string, string)`, `grabClipboard func() string`, `grabSelection func() string`, `grabScreen func() []byte` (tests replace these).

- [ ] **Step 1: Write the failing test**

Create `internal/context/capture_test.go`:
```go
package context

import (
	"strings"
	"testing"
)

func withFakes(t *testing.T, app, title, clip, sel string) {
	t.Helper()
	ow, oc, os_ := grabWindow, grabClipboard, grabSelection
	grabWindow = func() (string, string) { return app, title }
	grabClipboard = func() string { return clip }
	grabSelection = func() string { return sel }
	t.Cleanup(func() { grabWindow, grabClipboard, grabSelection = ow, oc, os_ })
}

func TestCaptureAmbientWithSelection(t *testing.T) {
	withFakes(t, "chrome.exe", "Inbox - Gmail", "clip text", "selected text")
	c := CaptureAmbient(true)
	if c.AppName != "chrome.exe" || c.WindowTitle != "Inbox - Gmail" {
		t.Errorf("window not captured: %+v", c)
	}
	if c.Clipboard != "clip text" || c.Selection != "selected text" {
		t.Errorf("clip/sel not captured: %+v", c)
	}
	s := c.String()
	for _, want := range []string{"chrome.exe", "Inbox - Gmail", "clip text", "selected text"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() missing %q: %s", want, s)
		}
	}
}

func TestCaptureAmbientSkipsSelection(t *testing.T) {
	called := false
	withFakes(t, "a", "b", "c", "d")
	grabSelection = func() string { called = true; return "d" }
	if c := CaptureAmbient(false); c.Selection != "" || called {
		t.Errorf("withSelection=false must not grab selection (got %q, called=%v)", c.Selection, called)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/context/ -run TestCaptureAmbient -v`
Expected: FAIL — undefined `CaptureAmbient`/`grab*`.

- [ ] **Step 3: Implement**

Create `internal/context/capture.go`:
```go
package context

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/go-vgo/robotgo"
	"github.com/yourname/voice-agent/internal/executor"
)

// Capture is the ambient desktop context attached to a Tier-1 request.
type Capture struct {
	AppName     string
	WindowTitle string
	Clipboard   string
	Selection   string
	Screenshot  []byte
}

const clipMax = 2000

// Overridable grabbers (replaced in tests).
var (
	grabWindow = func() (string, string) {
		wc, err := GetActiveWindowContext()
		if err != nil || wc == nil {
			return "", ""
		}
		return wc.ProcessName, wc.WindowTitle
	}
	grabClipboard = func() string {
		s, _ := clipboard.ReadAll()
		return s
	}
	// grabSelection copies the current selection WITHOUT clobbering the clipboard.
	grabSelection = func() string {
		saved, _ := clipboard.ReadAll()
		robotgo.KeyTap("c", "ctrl")
		time.Sleep(80 * time.Millisecond) // short settle; keep the bar snappy
		sel, _ := clipboard.ReadAll()
		_ = clipboard.WriteAll(saved) // restore, best-effort
		if sel == saved {
			return "" // nothing newly selected
		}
		return sel
	}
	grabScreen = func() []byte {
		b, err := executor.CaptureScreen()
		if err != nil {
			return nil
		}
		return b
	}
)

func truncate(s string) string {
	if len(s) > clipMax {
		return s[:clipMax] + "… (truncated)"
	}
	return s
}

// CaptureAmbient grabs window + clipboard always; selection only when withSelection.
func CaptureAmbient(withSelection bool) Capture {
	app, title := grabWindow()
	c := Capture{AppName: app, WindowTitle: title, Clipboard: truncate(grabClipboard())}
	if withSelection {
		c.Selection = truncate(grabSelection())
	}
	return c
}

// WithScreenshot returns a copy with the current screen captured.
func (c Capture) WithScreenshot() Capture {
	c.Screenshot = grabScreen()
	return c
}

// String renders the text context block for the LLM system prompt (empty fields omitted).
func (c Capture) String() string {
	var b strings.Builder
	add := func(label, v string) {
		if strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, v)
		}
	}
	add("Active App", c.AppName)
	add("Window Title", c.WindowTitle)
	add("Selected Text", c.Selection)
	add("Clipboard", c.Clipboard)
	return strings.TrimSpace(b.String())
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/context/ -run TestCaptureAmbient -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/context/capture.go internal/context/capture_test.go
git commit -m "feat(context): add ambient Capture (window/clipboard/non-destructive selection)"
```

---

### Task 2: `NeedsScreenshot` heuristic

**Files:**
- Create: `internal/context/visual.go`, `internal/context/visual_test.go`

**Interfaces:**
- Produces: `func NeedsScreenshot(instruction string) bool`.

- [ ] **Step 1: Write the failing test**

Create `internal/context/visual_test.go`:
```go
package context

import "testing"

func TestNeedsScreenshot(t *testing.T) {
	visual := []string{
		"what's on my screen", "explain this error", "what am i looking at",
		"summarize what you see", "read the screen", "what is this on my display",
	}
	for _, s := range visual {
		if !NeedsScreenshot(s) {
			t.Errorf("%q should need a screenshot", s)
		}
	}
	textual := []string{
		"summarize this", "reply to this email", "what time is it", "open notepad",
	}
	for _, s := range textual {
		if NeedsScreenshot(s) {
			t.Errorf("%q should NOT need a screenshot", s)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/context/ -run TestNeedsScreenshot -v`
Expected: FAIL — undefined `NeedsScreenshot`.

- [ ] **Step 3: Implement**

Create `internal/context/visual.go`:
```go
package context

import "strings"

// visualCues are phrases that imply the user is asking about on-screen visual content.
var visualCues = []string{
	"on my screen", "on screen", "on the screen", "on my display",
	"what you see", "what do you see", "read the screen", "the screen",
	"this error", "what am i looking at", "look at this", "see this", "screenshot",
}

// NeedsScreenshot reports whether an instruction needs a screen capture for context.
func NeedsScreenshot(instruction string) bool {
	l := strings.ToLower(instruction)
	for _, cue := range visualCues {
		if strings.Contains(l, cue) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/context/ -v` (all pass)
```bash
git add internal/context/visual.go internal/context/visual_test.go
git commit -m "feat(context): add NeedsScreenshot visual-intent heuristic"
```

---

## Phase B — Thread context through dispatch + orchestrator

### Task 3: `Orchestrator.Run` gains `sysContext`

**Files:**
- Modify: `internal/agent/orchestrator.go`
- Test: `internal/agent/orchestrator_test.go` (create)

**Interfaces:**
- Consumes: `llm.Provider.Generate(ctx, prompt string, images [][]byte) (string, error)`.
- Produces: `func (o *Orchestrator) Run(ctx context.Context, userText, sysContext string) error`; `decompose(ctx, userText, sysContext string)`.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/orchestrator_test.go`:
```go
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/yourname/voice-agent/internal/llm"
)

type capturingProvider struct{ lastPrompt string }

func (p *capturingProvider) GenerateIntent(context.Context, llm.IntentRequest) (llm.IntentResponse, error) {
	return llm.IntentResponse{}, nil
}
func (p *capturingProvider) StreamGenerateIntent(context.Context, llm.IntentRequest, chan<- string) (llm.IntentResponse, error) {
	return llm.IntentResponse{}, nil
}
func (p *capturingProvider) ClassifyAndPlan(context.Context, string, string, string) (llm.ClassifyResponse, error) {
	return llm.ClassifyResponse{}, nil
}
func (p *capturingProvider) Generate(_ context.Context, prompt string, _ [][]byte) (string, error) {
	p.lastPrompt = prompt
	return "[]", nil // empty decompose -> orchestrator falls back, but prompt is captured
}

func TestRunForwardsSysContextToDecompose(t *testing.T) {
	p := &capturingProvider{}
	orch := NewOrchestrator(p, NewExecutor(nil))
	// Empty decompose result makes execSubGoal run google_workflow_agent against a nil
	// registry -> ExecutePlan errors; we only assert the decompose prompt carried context.
	_ = orch.Run(context.Background(), "reply to this", "Selected Text: hello world")
	if !strings.Contains(p.lastPrompt, "hello world") {
		t.Errorf("decompose prompt missing sysContext; got: %s", p.lastPrompt)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/agent/ -run TestRunForwardsSysContext -v`
Expected: FAIL — `Run` takes 2 args (compile error).

- [ ] **Step 3: Implement**

In `internal/agent/orchestrator.go`:
1. Change the signature and the decompose call:
```go
func (o *Orchestrator) Run(ctx gocontext.Context, userText, sysContext string) error {
	ui.ShowNotification("Planning your request…")
	log.Printf("[Orchestrator] Decomposing: %q\n", userText)

	subGoals, err := o.decompose(ctx, userText, sysContext)
```
2. In the single-goal fast path and the loop, inject `sysContext` into each sub-goal's `Context` when empty:
```go
	if len(subGoals) == 1 {
		if subGoals[0].Context == "" {
			subGoals[0].Context = sysContext
		}
		return o.execSubGoal(ctx, subGoals[0])
	}
	...
	for i, sg := range subGoals {
		if sg.Context == "" {
			sg.Context = sysContext
		}
		...
```
3. Change `decompose` and the prompt to include context:
```go
func (o *Orchestrator) decompose(ctx gocontext.Context, userText, sysContext string) ([]SubGoal, error) {
	prompt := fmt.Sprintf(decompositionPrompt, sysContext, userText)
	raw, err := o.Provider.Generate(ctx, prompt, nil)
	...
}
```
4. Update `decompositionPrompt` to add a context block before the user request (two `%s` now — context first, then request):
```go
const decompositionPrompt = `You are a task decomposition engine for a voice assistant.
... (unchanged agent list + rules) ...

Desktop context (may be empty):
%s

User request: %s

Return ONLY the JSON array.`
```
(Keep the existing agents/rules text intact; only add the "Desktop context" block and the leading `%s`.)

- [ ] **Step 4: Run to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/agent/ -run TestRunForwardsSysContext -v`
Expected: PASS.

- [ ] **Step 5: Fix callers so the tree compiles**

`Run` now needs 3 args. Update the two existing callers (leave their behavior otherwise unchanged; Task 4 will pass real context from dispatch, Task 5 keeps RunAICommand's existing context string):
- `internal/dispatch/dispatch.go`: `orch.Run(ctx, input)` → `orch.Run(ctx, input, "")` (Task 4 replaces `""`).
- `internal/command/router.go` `RunAICommand`: `orch.Run(globalCtx, prompt)` → `orch.Run(globalCtx, prompt, contextStr)` (reuse the `contextStr` already built in that function).

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/...` (exit 0).

- [ ] **Step 6: Commit**

```bash
git add internal/agent/orchestrator.go internal/agent/orchestrator_test.go internal/dispatch/dispatch.go internal/command/router.go
git commit -m "feat(agent): thread sysContext through Orchestrator.Run + decompose"
```

---

### Task 4: `dispatch.Handle` takes `Capture`; Tier-1 text + visual paths

**Files:**
- Modify: `internal/dispatch/dispatch.go`, `internal/dispatch/dispatch_test.go`

**Interfaces:**
- Consumes: `agentctx.Capture`, `agentctx.NeedsScreenshot`, `llm.Provider.Generate`.
- Produces: `func (d *Deps) Handle(ctx context.Context, input string, cap agentctx.Capture) error`.

- [ ] **Step 1: Update the existing SP1 test to the new signature + add coverage**

In `internal/dispatch/dispatch_test.go`: add import `agentctx "github.com/yourname/voice-agent/internal/context"`. Change every `d.Handle(ctx, "...", "")` call to `d.Handle(ctx, "...", agentctx.Capture{})`. Then append:
```go
func TestHandleTier1PassesContextToProvider(t *testing.T) {
	prov := &recordingProvider{}
	reg := tools.DefaultRegistry(prov)
	profile := security.DeveloperProfile()
	d := &Deps{Registry: reg, Provider: prov, Profile: &profile, Resolver: resolver.NewResolver()} // no matchers -> Tier 1
	cap := agentctx.Capture{AppName: "chrome.exe", Selection: "hello world"}
	_ = d.Handle(context.Background(), "reply to this", cap)
	if !prov.called {
		t.Fatal("Tier 1 must call the provider")
	}
}
```
(`recordingProvider` from the SP1 test records `called`; here we WANT it called on Tier 1. If `recordingProvider.Generate` currently returns `"[]"`, keep it.)

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/dispatch/ -v`
Expected: FAIL — `Handle` signature mismatch / compile error.

- [ ] **Step 3: Implement**

In `internal/dispatch/dispatch.go`:
1. Add import `agentctx "github.com/yourname/voice-agent/internal/context"`.
2. Change the signature and Tier-1 branch:
```go
func (d *Deps) Handle(ctx context.Context, input string, cap agentctx.Capture) error {
	if d.Resolver == nil || d.Registry == nil {
		return fmt.Errorf("dispatch: Deps not fully configured")
	}
	norm := resolver.Normalize(input, cap.AppName)
	if match, ok := d.Resolver.Resolve(norm); ok {
		atomic.AddInt64(&localHits, 1)
		log.Printf("[dispatch] TIER0 (%s) %q -> %d task(s)", match.Reason, input, len(match.Tasks))
		if err := d.enforceSecurity(match.Tasks); err != nil {
			return err
		}
		exec := agent.NewExecutor(d.Registry)
		return exec.ExecutePlan(ctx, agent.Plan{Transcript: input, Intent: "local_resolve", Tasks: match.Tasks})
	}
	atomic.AddInt64(&cloudHits, 1)

	// Tier 1: visual sub-path when the request implies on-screen context.
	if agentctx.NeedsScreenshot(input) {
		log.Printf("[dispatch] TIER1 (visual) %q", input)
		shot := cap.WithScreenshot()
		prompt := fmt.Sprintf("Desktop context:\n%s\n\nUser request: %s\n\nAnswer helpfully and concisely.",
			shot.String(), input)
		answer, err := d.Provider.Generate(ctx, prompt, [][]byte{shot.Screenshot})
		if err != nil {
			return err
		}
		ui.ShowOutputOverlay(answer)
		return nil
	}

	// Tier 1: text path via the orchestrator, enriched with captured context.
	log.Printf("[dispatch] TIER1 (cloud) %q", input)
	exec := agent.NewExecutor(d.Registry)
	orch := agent.NewOrchestrator(d.Provider, exec)
	return orch.Run(ctx, input, cap.String())
}
```
3. Add imports `"fmt"` (if missing) and `"github.com/yourname/voice-agent/internal/ui"`.
4. Revert the Task-3 stopgap `orch.Run(ctx, input, "")` — it's replaced by the code above.

- [ ] **Step 4: Run to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/dispatch/ -v`
Expected: PASS (all SP1 tests + the new one).

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/dispatch.go internal/dispatch/dispatch_test.go
git commit -m "feat(dispatch): Handle takes Capture; Tier-1 text+visual context paths"
```

---

## Phase C — Capture at the invocation points

### Task 5: Capture on the text path (Ctrl+Space)

**Files:**
- Modify: `internal/command/hotkey.go`, `internal/command/router.go`

**Interfaces:**
- Produces: `command.SetPendingCapture(agentctx.Capture)` / `command.takePendingCapture() agentctx.Capture` (package-internal handoff from keypress to `ProcessCommand`).

- [ ] **Step 1: Capture at the keypress (before the bar shows)**

In `internal/command/hotkey.go`, add imports `agentctx "github.com/yourname/voice-agent/internal/context"` and `"sync"`. Add a guarded pending-capture slot and set it right before showing the bar:
```go
var (
	pendingMu      sync.Mutex
	pendingCapture *agentctx.Capture
)

func setPendingCapture(c agentctx.Capture) {
	pendingMu.Lock()
	pendingCapture = &c
	pendingMu.Unlock()
}

// takePendingCapture returns the capture stashed at the last hotkey press (and clears it),
// or a fresh cheap capture (no selection) if none is pending.
func takePendingCapture() agentctx.Capture {
	pendingMu.Lock()
	c := pendingCapture
	pendingCapture = nil
	pendingMu.Unlock()
	if c != nil {
		return *c
	}
	return agentctx.CaptureAmbient(false)
}
```
In the `Ctrl+Space` handler, capture BEFORE `ui.ShowCommandBar()`:
```go
		if event.Message == types.WM_KEYDOWN && event.VKCode == types.VK_SPACE && ctrlDown {
			log.Println("Command palette triggered via hotkey!")
			setPendingCapture(agentctx.CaptureAmbient(true)) // target app still has focus here
			ui.ShowCommandBar()
		}
```

- [ ] **Step 2: Consume it in ProcessCommand**

In `internal/command/router.go` `ProcessCommand`, replace the current context-building + `globalDispatch.Handle(globalCtx, input, activeApp)` block with:
```go
	cap := takePendingCapture()
	if err := globalDispatch.Handle(globalCtx, input, cap); err != nil {
		log.Printf("dispatch failed: %v", err)
	}
```
Remove the now-unused `agentctx.BuildContext()`/`activeApp` lines in `ProcessCommand` (leave `RunAICommand` and its `contextStr` intact).

- [ ] **Step 3: Verify compile**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/... && go vet ./internal/command/`
Expected: exit 0 / clean.

- [ ] **Step 4: Commit**

```bash
git add internal/command/hotkey.go internal/command/router.go
git commit -m "feat(command): capture ambient context at Ctrl+Space keypress"
```

---

### Task 6: Capture on the voice path

**Files:**
- Modify: `internal/engine/runtime.go`

**Interfaces:**
- Produces: a transcribed-event payload carrying the capture: `type transcribedPayload struct { Transcript string; Cap agentctx.Capture }`.

- [ ] **Step 1: Capture at trigger, carry it to the transcribed handler**

In `internal/engine/runtime.go`:
1. Add import `agentctx "github.com/yourname/voice-agent/internal/context"`.
2. Add the payload type near the other event types:
```go
type transcribedPayload struct {
	Transcript string
	Cap        agentctx.Capture
}
```
3. In the `EventVoiceInput` goroutine, capture ambient context (selection included; overlaps recording so no latency cost) and carry it into the transcribed event. Locate where it currently emits `Event{Type: EventTranscribed, Payload: transcript}` and change the surrounding code to:
```go
		go func() {
			cap := agentctx.CaptureAmbient(true) // app still focused (pill never steals focus)
			audioData, err := audio.RecordDynamic(10*time.Second, 0.01, 32000)
			// ... existing length checks unchanged ...
			// ... existing asr.Transcribe(...) -> transcript ...
			e.Events <- Event{Type: EventTranscribed, Payload: transcribedPayload{Transcript: transcript, Cap: cap}}
		}()
```
(Adjust to the actual code between record and emit — only the capture line and the payload change.)

- [ ] **Step 2: Use the capture in the transcribed handler**

In `handleEvent`, the `EventTranscribed` case currently does `transcript := ev.Payload.(string)` and calls `e.Dispatch.Handle(ctx, transcript, activeApp)`. Replace with:
```go
	case EventTranscribed:
		p := ev.Payload.(transcribedPayload)
		fmt.Printf("📝 Transcript: %s\n", p.Transcript)
		go func() {
			ui.SetState(ui.StateExecuting)
			if err := e.Dispatch.Handle(ctx, p.Transcript, p.Cap); err != nil {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("dispatch failed: %w", err)}
				audit.LogAction(p.Transcript, "dispatch", nil, "FAILED: "+err.Error())
				return
			}
			audit.LogAction(p.Transcript, "dispatch", nil, "SUCCESS")
			e.Events <- Event{Type: EventToolExecuted, Payload: agent.Plan{Transcript: p.Transcript, Intent: "dispatch"}}
		}()
```
Remove the now-unused `agentctx.BuildContext()` activeApp lines from this case if present.

- [ ] **Step 3: Verify compile**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/... && go vet ./internal/engine/`
Expected: exit 0 / clean.

- [ ] **Step 4: Commit**

```bash
git add internal/engine/runtime.go
git commit -m "feat(engine): capture ambient context at voice trigger, carry into dispatch"
```

---

## Phase D — Wake word

### Task 7: Testable wake loop (pure Go) + build-tagged wiring + stub

**Files:**
- Create: `internal/wakeword/loop.go`, `internal/wakeword/loop_test.go`, `internal/wakeword/stub.go`
- Modify: `internal/wakeword/porcupine.go`

**Interfaces:**
- Produces:
  - `type FrameSource interface { Read() ([]int16, error); Start() error; Stop() error }`
  - `type Detector interface { Process(frame []int16) (int, error) }`
  - `func runWakeLoop(ctx context.Context, src FrameSource, det Detector, onDetect func()) error`
  - `func StartWakeWordLoop(ctx context.Context, accessKey string, onDetect func()) error` (real under `whisper`, no-op under `!whisper`).

- [ ] **Step 1: Write the failing test (pure Go, no CGO)**

Create `internal/wakeword/loop_test.go`:
```go
package wakeword

import (
	"context"
	"testing"
	"time"
)

type fakeSource struct{ started, stopped int }

func (f *fakeSource) Read() ([]int16, error) { return []int16{0}, nil }
func (f *fakeSource) Start() error           { f.started++; return nil }
func (f *fakeSource) Stop() error            { f.stopped++; return nil }

// detector that fires the keyword once, then never again.
type onceDetector struct{ fired bool }

func (d *onceDetector) Process([]int16) (int, error) {
	if !d.fired {
		d.fired = true
		return 0, nil // keyword index 0 = detected
	}
	return -1, nil
}

func TestRunWakeLoopHandsOffMicAroundOnDetect(t *testing.T) {
	src := &fakeSource{}
	det := &onceDetector{}
	ctx, cancel := context.WithCancel(context.Background())

	order := []string{}
	onDetect := func() {
		// When onDetect runs, the mic must already be released.
		if src.stopped != 1 {
			t.Errorf("recorder not stopped before onDetect (stopped=%d)", src.stopped)
		}
		order = append(order, "detect")
		cancel() // end the loop after one detection
	}

	err := runWakeLoop(ctx, src, det, onDetect)
	if err != nil {
		t.Fatalf("runWakeLoop returned error: %v", err)
	}
	if src.stopped < 1 {
		t.Errorf("expected Stop() around onDetect")
	}
}

func TestRunWakeLoopExitsOnCancel(t *testing.T) {
	src := &fakeSource{}
	det := &onceDetector{fired: true} // never detects
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := runWakeLoop(ctx, src, det, func() {}); err != nil {
		t.Fatalf("expected clean exit on cancel, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/wakeword/ -run TestRunWakeLoop -v`
Expected: FAIL — undefined `runWakeLoop`/interfaces.

- [ ] **Step 3: Implement the pure loop**

Create `internal/wakeword/loop.go` (NO build tag — pure Go, testable everywhere):
```go
package wakeword

import "context"

// FrameSource yields audio frames and controls mic capture.
type FrameSource interface {
	Read() ([]int16, error)
	Start() error
	Stop() error
}

// Detector processes a frame and returns a keyword index >= 0 on a wake-word hit.
type Detector interface {
	Process(frame []int16) (int, error)
}

// runWakeLoop reads frames until ctx is cancelled. On a wake-word hit it Stops the source
// (releasing the mic), runs onDetect (which must BLOCK until the command finishes), then
// Starts the source again. This is the microphone baton.
func runWakeLoop(ctx context.Context, src FrameSource, det Detector, onDetect func()) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frame, err := src.Read()
		if err != nil {
			continue // transient read error; keep listening
		}
		idx, err := det.Process(frame)
		if err != nil {
			continue
		}
		if idx >= 0 {
			_ = src.Stop()
			onDetect() // blocks until the command completes
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if err := src.Start(); err != nil {
				return err
			}
		}
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/wakeword/ -run TestRunWakeLoop -v`
Expected: PASS.

- [ ] **Step 5: Build-tag the real wiring + add the stub**

Add `//go:build whisper` as the FIRST line of `internal/wakeword/porcupine.go` (blank line after), then REPLACE its `ListenForWakeWord` with the real `StartWakeWordLoop` that adapts pvrecorder + porcupine to the interfaces:
```go
//go:build whisper

package wakeword

import (
	"context"
	"fmt"

	porcupine "github.com/Picovoice/porcupine/binding/go/v3"
	pvrecorder "github.com/Picovoice/pvrecorder/binding/go"
)

type pvSource struct{ rec *pvrecorder.PvRecorder }

func (s *pvSource) Read() ([]int16, error) { return s.rec.Read() }
func (s *pvSource) Start() error           { return s.rec.Start() }
func (s *pvSource) Stop() error            { s.rec.Stop(); return nil }

type ppDetector struct{ p *porcupine.Porcupine }

func (d *ppDetector) Process(f []int16) (int, error) { return d.p.Process(f) }

// StartWakeWordLoop listens for the built-in "Porcupine" keyword until ctx is cancelled.
func StartWakeWordLoop(ctx context.Context, accessKey string, onDetect func()) error {
	p := porcupine.Porcupine{
		AccessKey:       accessKey,
		BuiltInKeywords: []porcupine.BuiltInKeyword{porcupine.PORCUPINE},
	}
	if err := p.Init(); err != nil {
		return fmt.Errorf("porcupine init: %w", err)
	}
	defer p.Delete()

	rec := pvrecorder.NewPvRecorder(porcupine.FrameLength)
	rec.DeviceIndex = -1
	if err := rec.Init(); err != nil {
		return fmt.Errorf("pvrecorder init: %w", err)
	}
	defer rec.Delete()
	if err := rec.Start(); err != nil {
		return fmt.Errorf("pvrecorder start: %w", err)
	}
	fmt.Println("🎙️  Wake word active — say 'Porcupine'")
	return runWakeLoop(ctx, &pvSource{rec}, &ppDetector{&p}, onDetect)
}
```
Create `internal/wakeword/stub.go`:
```go
//go:build !whisper

package wakeword

import "context"

// StartWakeWordLoop is a no-op in the default (no-voice) build.
func StartWakeWordLoop(ctx context.Context, accessKey string, onDetect func()) error {
	<-ctx.Done()
	return nil
}
```

- [ ] **Step 6: Verify both build variants compile the package**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/wakeword/` (default/stub, exit 0)
Run: `export PATH="$PATH:/c/w64devkit/bin" && go vet -tags whisper ./internal/wakeword/` (real wiring compiles; if it fails to LINK native libs that's Task 10's concern — `go vet` compiles without linking an exe)

- [ ] **Step 7: Commit**

```bash
git add internal/wakeword/loop.go internal/wakeword/loop_test.go internal/wakeword/porcupine.go internal/wakeword/stub.go
git commit -m "feat(wakeword): testable mic-handoff loop + build-tagged Porcupine wiring + stub"
```

---

### Task 8: Engine command-completion signal

**Files:**
- Modify: `internal/engine/runtime.go`

**Interfaces:**
- Produces: `func (e *Engine) TriggerAndWait(timeout time.Duration)` — fires a voice capture like the pill and blocks until the command completes (used by the wake-word `onDetect`).

- [ ] **Step 1: Add a completion channel + trigger-and-wait**

In `internal/engine/runtime.go`:
1. Add a field to the `Engine` struct: `commandDone chan struct{}`.
2. In `NewEngine`, initialize it in the returned literal: `commandDone: make(chan struct{}, 1),`.
3. In `handleEvent`, in BOTH the `EventToolExecuted` and `EventError` cases, after clearing `isBusy`, signal completion (non-blocking):
```go
		select {
		case e.commandDone <- struct{}{}:
		default:
		}
```
4. Add the public helper:
```go
// TriggerAndWait fires a voice capture (as if the pill were clicked) and blocks until the
// command finishes or timeout elapses. Used by the wake-word loop to hand the mic back only
// after the command is done.
func (e *Engine) TriggerAndWait(timeout time.Duration) {
	// drain any stale completion signal
	select {
	case <-e.commandDone:
	default:
	}
	select {
	case ui.ListenTrigger <- struct{}{}:
	case <-time.After(2 * time.Second):
		return // engine not consuming triggers; give up
	}
	select {
	case <-e.commandDone:
	case <-time.After(timeout):
	}
}
```

- [ ] **Step 2: Verify compile**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/... && go vet ./internal/engine/`
Expected: exit 0 / clean.

- [ ] **Step 3: Commit**

```bash
git add internal/engine/runtime.go
git commit -m "feat(engine): command-completion signal + TriggerAndWait for wake word"
```

---

### Task 9: Start the wake loop in main.go

**Files:**
- Modify: `cmd/app/main.go`

- [ ] **Step 1: Start the wake-word loop (config-gated)**

In `cmd/app/main.go`, after the engine is created and `engineApp.Start(rootCtx)` is launched, add (READ main.go for the exact engine variable name; the plan assumes `engineApp`):
```go
	if cfg.EnableVoice && cfg.PorcupineAccessKey != "" {
		go func() {
			onDetect := func() { engineApp.TriggerAndWait(60 * time.Second) }
			if err := wakeword.StartWakeWordLoop(rootCtx, cfg.PorcupineAccessKey, onDetect); err != nil {
				log.Printf("wake word stopped: %v", err)
			}
		}()
	}
```
Add import `"github.com/yourname/voice-agent/internal/wakeword"`. (In the default build this calls the stub, which blocks on ctx and returns — harmless. The real loop only links under `-tags whisper`.)

- [ ] **Step 2: Verify both builds**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go vet ./cmd/app/` (default/stub compiles, exit 0)

- [ ] **Step 3: Commit**

```bash
git add cmd/app/main.go
git commit -m "feat: start wake-word loop when voice enabled + porcupine key set"
```

---

## Phase E — Build, verify, docs

### Task 10: Full verification + voice build + docs

**Files:**
- Modify: `docs/BUILD-VOICE.md`

- [ ] **Step 1: Non-voice suite + vet**

Run:
```bash
export PATH="$PATH:/c/w64devkit/bin"
go vet ./cmd/... ./internal/...
go test -count=1 ./internal/context/ ./internal/dispatch/ ./internal/resolver/ ./internal/agent/ ./internal/wakeword/
```
Expected: vet clean; all tests PASS.

- [ ] **Step 2: Build the voice exe (links Porcupine + whisper)**

Run:
```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -tags whisper -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
```
Expected: exit 0. If the linker fails on Porcupine/pvrecorder native libs (`undefined reference` / `cannot find -lpv_porcupine`), resolve like whisper: ensure the Porcupine/pvrecorder libs+DLLs are present and toolchain-matched, and record the fix in `docs/BUILD-VOICE.md`. Do NOT mark this task done until the voice exe links.

- [ ] **Step 3: Update docs**

In `docs/BUILD-VOICE.md`, add a "Wake word" section: it's included in the `-tags whisper` voice build, started when `enable_voice=true` and `porcupine_access_key` is set; keyword is the built-in "Porcupine"; note any native-lib steps discovered in Step 2.

- [ ] **Step 4: Manual/headless verification**

Confirm the ambient-context path end-to-end (reuse the SP1 TTS round-trip idea): seed a selection, invoke a Tier-1 request ("summarize this") and confirm the provider prompt received the selection. Confirm the default build still runs voice-free (`go build -o voice-agent-lean.exe ./cmd/app` links; wake-word stub is a no-op). Note results.

- [ ] **Step 5: Commit**

```bash
git add docs/BUILD-VOICE.md
git commit -m "docs(sp2): wake-word build/run notes; SP2 verification pass"
```

---

## Self-Review (completed by plan author)

- **Spec coverage:** Feature A capture unit → Tasks 1–2; Tier-1 text/visual context → Tasks 3–4; capture at invocations (voice + Ctrl+Space) → Tasks 5–6; Feature B wake loop + mic hand-off → Task 7; completion signal → Task 8; config-gated start → Task 9; build/verify/docs → Task 10. Tier-0-stays-instant is enforced in Task 4 (resolver-first, cap untouched on match). Non-destructive selection → Task 1. Build tag → Task 7. No spec section uncovered.
- **Placeholder scan:** no TBD/TODO; every code step has real code. Two "read the actual code and adjust" guardrails (the record→emit region in Task 6, the engine var name in Task 9) are anchors, not placeholders.
- **Type consistency:** `Capture`/`CaptureAmbient`/`WithScreenshot`/`String`/`NeedsScreenshot`, `Handle(ctx,input,cap)`, `Orchestrator.Run(ctx,userText,sysContext)`, `runWakeLoop`/`StartWakeWordLoop`/`FrameSource`/`Detector`, `TriggerAndWait`, `transcribedPayload` are used identically across producing and consuming tasks.
