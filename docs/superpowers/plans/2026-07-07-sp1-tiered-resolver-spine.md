# SP1 — Tiered Resolver Spine + Overhead Cut — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a deterministic local-first resolution tier so common commands run instantly, offline, and token-free, with the cloud LLM as a genuine fallback — while stripping the codebase to a lean base and fixing two correctness bugs.

**Architecture:** A new `internal/resolver` package runs a prioritized chain of deterministic `Matcher`s, each returning a confidence score. A new `internal/dispatch` package is the single entry point both the voice path (`engine`) and text path (`command`) call: on a high-confidence match it runs the resulting `agent.Task`s directly through the existing `GraphExecutor` (Tier 0); otherwise it falls back to the existing `Orchestrator` cloud path (Tier 1). Security/confirmation is enforced once, centrally.

**Tech Stack:** Go 1.26, CGO (sqlite/whisper/robotgo), `go-vgo/robotgo` for input, Windows `rundll32`/PowerShell for system actions, existing `internal/tools` registry + `internal/agent` executor.

## Global Constraints

- Module path: `github.com/yourname/voice-agent`. All new packages live under `internal/`.
- Compute is **cloud-LLM-only (BYOK)** — introduce **no** local ML model.
- Tier 0 must make **zero network calls** and complete in **< 50 ms** for the common-command test set.
- No behavior is silently dropped: any command not confidently resolved locally MUST fall through to the existing Tier-1 cloud path.
- Windows-only is acceptable (consistent with the existing app).
- Confidence threshold constant: **0.7** (tunable in one place).
- Follow existing test style (`internal/tools/research_tool_test.go`), table-driven where practical. Commit after every task.
- Cleanup policy is **surgical**: delete pure cruft; keep dormant SP3/SP4 code marked "planned, not wired".

---

## File Structure

**New:**
- `internal/resolver/resolver.go` — `NormalizedInput`, `Normalize`, `Match`, `Matcher`, `Resolver`, `NewResolver`, `Resolve`.
- `internal/resolver/matchers.go` — the seven concrete matchers + `Default()` wiring.
- `internal/resolver/resolver_test.go`, `internal/resolver/matchers_test.go`.
- `internal/dispatch/dispatch.go` — `Deps`, `Handle`, `enforceSecurity`, tier-usage counters.
- `internal/dispatch/dispatch_test.go`.
- `internal/tools/media_control.go`, `internal/tools/system_control.go`, `internal/tools/window_control.go` (+ registration).

**Modified:**
- `internal/command/router.go` — `ProcessCommand` routes non-`ai` input through dispatch.
- `internal/engine/runtime.go` — `EventTranscribed` routes through dispatch; delete `planExecution`.
- `internal/tools/registry.go` — register the three new tools.
- `internal/security/permissions.go` — allow the three new tools in `DeveloperProfile`.
- `internal/llm/openai.go`, `internal/llm/anthropic.go` — fix `ClassifyAndPlan` prompt.
- `internal/tools/spotify_ai.go` — fix malformed schema JSON.
- `cmd/app/main.go` — build the resolver + dispatch deps; lazy overlays; conditional voice init.
- `internal/ui/automation_overlay.go`, `internal/ui/highlight_overlay.go` — lazy first-use init.
- `README.md`, `CLAUDE.md` — build flags + architecture note.

**Deleted:** see Task 2 / Task 3.

---

## Phase A — Baseline, cleanup, bug fixes

### Task 1: Measure and record the overhead baseline

**Files:**
- Create: `docs/superpowers/plans/sp1-baseline.md`

- [ ] **Step 1: Build the current binary and record size**

Run:
```bash
cd "E:/Voice Agent"
go build -o voice-agent.exe ./cmd/app
ls -la voice-agent.exe | awk '{print $5}'
```
Record the byte size.

- [ ] **Step 2: Record idle RAM and cold start**

Launch `./voice-agent.exe`, wait until the pill overlay appears (cold-start wall-clock, eyeball or `Measure-Command` in PowerShell), then in PowerShell:
```powershell
Get-Process voice-agent | Select-Object WorkingSet64, PrivateMemorySize64
```
Record WorkingSet64 (idle RAM) and the cold-start seconds.

- [ ] **Step 3: Write the baseline doc**

Create `docs/superpowers/plans/sp1-baseline.md` with a table: `metric | before | after (filled at end)` for binary size, idle WorkingSet64, cold-start seconds. Fill the "before" column now.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/plans/sp1-baseline.md
git commit -m "chore(sp1): record overhead baseline (binary size, idle RAM, cold start)"
```

---

### Task 2: Delete cruft files

**Files:**
- Delete: stale backups, logs, author-time scripts, root scratch, generated asset dirs.

- [ ] **Step 1: Confirm the generated asset dirs are not referenced**

Run:
```bash
cd "E:/Voice Agent"
grep -rn "stitch_assets\|stitch_generated_screen" --include=*.go . || echo "NO GO REFERENCES"
grep -rn "go:embed" internal/ui | grep -i "stitch" || echo "NO EMBED REFERENCES"
```
Expected: `NO GO REFERENCES` and `NO EMBED REFERENCES`. If any reference exists, STOP and keep that dir.

- [ ] **Step 2: Delete cruft**

```bash
cd "E:/Voice Agent"
# backup snapshots (numeric suffix)
find . -type f -name "*.go.[0-9]*" -not -path "./whisper.cpp/*" -delete
# root logs / scratch
rm -f build.log build2.log build3.txt build_err.txt build_errors.txt \
      build_output.txt build_output_utf8.txt crash.log err.txt voice-out.log \
      screen_analysis.txt test_uia.go
# author-time HTML-patch scripts (not invoked by Go)
rm -f internal/ui/extract.py internal/ui/inject*.py internal/ui/fix_encoding.py \
      internal/ui/fix_palette.py internal/ui/update_ui.py internal/ui/update_cmd.py \
      internal/ui/inject_validator.py internal/ui/test_0.js internal/ui/test_1.js
# generated asset dirs (only if step 1 was clean)
rm -rf stitch_assets stitch_generated_screen
```

- [ ] **Step 3: Verify the build is still clean**

Run: `go build ./... 2>&1 | head -20`
Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore(sp1): delete stale backups, build logs, author-time scripts, scratch files"
```

---

### Task 3: Remove dead code symbols + mark dormant packages

**Files:**
- Modify: `internal/engine/runtime.go` (delete `planExecution` and now-unused imports)
- Delete: `internal/memory/pruner.go`, `internal/ui/output_overlay.go`
- Modify: `internal/tools/microsoft_outlook.go`, `internal/alerts/alerts.go`, `internal/llm/proxy.go` (header comments)

- [ ] **Step 1: Confirm `planExecution` is unreferenced**

Run:
```bash
cd "E:/Voice Agent"
grep -rn "planExecution" --include=*.go . 
```
Expected: only its definition in `internal/engine/runtime.go`. If called anywhere, STOP.

- [ ] **Step 2: Delete `planExecution`**

Delete the entire `func (e *Engine) planExecution(...) (agent.Plan, error) { ... }` block in `internal/engine/runtime.go` (starts at the `func (e *Engine) planExecution` line). After deleting, remove any imports it solely used (`agentctx`, `memory`, `intent`, `executor`, `security` may still be used elsewhere — only remove those the compiler flags).

- [ ] **Step 3: Confirm and delete duplicate pruner + stub overlay**

Run:
```bash
grep -rn "StartPruningScheduler\|PruneMemories\b" --include=*.go . | grep -v "pruner.go"
```
Expected: no results (the live prune is the inline ticker in `main.go`). Then:
```bash
rm -f internal/memory/pruner.go internal/ui/output_overlay.go
```
If `output_overlay.go` defines a symbol referenced elsewhere, run `grep -rn "ShowOutputOverlay" internal/ui` — that function lives in `overlay.go`, not the stub. Confirm before deleting.

- [ ] **Step 4: Mark dormant SP3/SP4 packages**

Add this comment as the first line under the `package` clause of `internal/alerts/alerts.go`:
```go
// DORMANT — planned for SP3 (Ambient Trigger Engine). Not wired into main.go yet.
```
Add under the `package` clause of `internal/tools/microsoft_outlook.go` (representative file for the suite):
```go
// DORMANT — Microsoft suite planned for SP4 (Trustworthy One-Shot Automation). Not registered yet.
```
Add under the `package` clause of `internal/llm/proxy.go`:
```go
// DORMANT — ProxyProvider + Fallback* config planned for a later reliability slice. Not wired.
```

- [ ] **Step 5: Verify build + commit**

Run: `go build ./... 2>&1 | head -20` (expected: no output)
```bash
git add -A
git commit -m "chore(sp1): remove dead planExecution/pruner/stub, mark dormant SP3/SP4 code"
```

---

### Task 4: Fix OpenAI/Anthropic `ClassifyAndPlan` prompt bug

**Files:**
- Modify: `internal/llm/openai.go` (`ClassifyAndPlan`)
- Modify: `internal/llm/anthropic.go` (`ClassifyAndPlan`)
- Test: `internal/llm/classify_test.go`

**Interfaces:**
- Consumes: `classifySystemPrompt` (package-level string in `gemini.go`), `stripMarkdownCodeBlock` (helper in `gemini.go`).
- Produces: corrected `ClassifyAndPlan` that uses the classify prompt and parses `{"needs_screen": bool}`.

- [ ] **Step 1: Write the failing test**

Create `internal/llm/classify_test.go`:
```go
package llm

import (
	"strings"
	"testing"
)

// The classify system prompt must instruct the model to emit needs_screen,
// and both non-Gemini providers must build their classify request from it
// (not from the planning prompt).
func TestClassifySystemPromptMentionsNeedsScreen(t *testing.T) {
	if !strings.Contains(classifySystemPrompt, "needs_screen") {
		t.Fatalf("classifySystemPrompt must reference needs_screen")
	}
}

// Guard against regressing to the planning prompt in the classify path.
// buildClassifyPrompt is the shared helper the providers must call.
func TestBuildClassifyPromptUsesClassifyPrompt(t *testing.T) {
	got := buildClassifyPrompt("{}", "ctx")
	if !strings.Contains(got, "needs_screen") {
		t.Fatalf("buildClassifyPrompt must be derived from classifySystemPrompt")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/llm/ -run TestBuildClassifyPrompt -v`
Expected: FAIL — `buildClassifyPrompt` undefined.

- [ ] **Step 3: Add the shared helper**

In `internal/llm/gemini.go`, next to `buildPlanningPrompt`, add:
```go
// buildClassifyPrompt renders the fast-path classification prompt (needs_screen decision).
// Shared by all providers so the classify path never accidentally uses the planning prompt.
func buildClassifyPrompt(toolSchemas, systemContext string) string {
	return fmt.Sprintf(classifySystemPrompt, toolSchemas, systemContext)
}
```
(If `classifySystemPrompt` has a different number of `%s` verbs, match them exactly — read the const first and adjust the args.)

- [ ] **Step 4: Fix OpenAI `ClassifyAndPlan`**

In `internal/llm/openai.go`, in `ClassifyAndPlan`, replace the line that builds the system message from `buildPlanningPrompt(..., false)` with `buildClassifyPrompt(toolSchemas, systemContext)`. Then replace the substring `needs_screen` detection with a real parse:
```go
	var probe struct {
		NeedsScreen bool `json:"needs_screen"`
	}
	cleaned := stripMarkdownCodeBlock(content)
	if err := json.Unmarshal([]byte(cleaned), &probe); err != nil {
		// Safe fallback: if we cannot tell, take the screen path.
		return ClassifyResponse{NeedsScreen: true}, nil
	}
	if probe.NeedsScreen {
		return ClassifyResponse{NeedsScreen: true}, nil
	}
	return ClassifyResponse{NeedsScreen: false, RawJSON: cleaned}, nil
```

- [ ] **Step 5: Apply the identical fix to Anthropic**

In `internal/llm/anthropic.go` `ClassifyAndPlan`, make the same two changes (use `buildClassifyPrompt`, parse `needs_screen` with the safe fallback).

- [ ] **Step 6: Run tests + build**

Run: `go test ./internal/llm/ -v 2>&1 | tail -20` (expected: PASS)
Run: `go build ./... 2>&1 | head -5` (expected: no output)

- [ ] **Step 7: Commit**

```bash
git add internal/llm/
git commit -m "fix(llm): openai/anthropic ClassifyAndPlan now use the classify prompt and parse needs_screen"
```

---

### Task 5: Fix the malformed Spotify tool schema

**Files:**
- Modify: `internal/tools/spotify_ai.go` (`SpotifySmartRecommendTool.Parameters`)
- Test: `internal/tools/spotify_schema_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tools/spotify_schema_test.go`:
```go
package tools

import (
	"encoding/json"
	"testing"
)

func TestSpotifySmartRecommendSchemaIsValidJSON(t *testing.T) {
	tool := &SpotifySmartRecommendTool{}
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(tool.Parameters()), &v); err != nil {
		t.Fatalf("SpotifySmartRecommendTool.Parameters() is not valid JSON: %v", err)
	}
}
```
(If `SpotifySmartRecommendTool` requires fields to construct, use `&SpotifySmartRecommendTool{}` — `Parameters()` must not depend on state.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tools/ -run TestSpotifySmartRecommendSchemaIsValidJSON -v`
Expected: FAIL — invalid JSON (missing comma).

- [ ] **Step 3: Fix the schema string**

Open `internal/tools/spotify_ai.go`, find `SpotifySmartRecommendTool.Parameters()`, and add the missing comma after the `action` property object so the JSON parses. Verify by eye that every property object except the last is comma-separated.

- [ ] **Step 4: Run test + commit**

Run: `go test ./internal/tools/ -run TestSpotifySmartRecommendSchemaIsValidJSON -v` (expected: PASS)
```bash
git add internal/tools/spotify_ai.go internal/tools/spotify_schema_test.go
git commit -m "fix(tools): correct malformed JSON schema in spotify_smart_recommend"
```

---

## Phase B — Resolver core

### Task 6: `NormalizedInput` + `Normalize`

**Files:**
- Create: `internal/resolver/resolver.go`
- Test: `internal/resolver/resolver_test.go`

**Interfaces:**
- Produces: `type NormalizedInput struct{ Raw, Lower, ActiveApp string; Tokens []string }`, `func Normalize(raw, activeApp string) NormalizedInput`.

- [ ] **Step 1: Write the failing test**

Create `internal/resolver/resolver_test.go`:
```go
package resolver

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	in := Normalize("  Open  Notepad  ", "chrome.exe")
	if in.Lower != "open notepad" {
		t.Errorf("Lower = %q, want %q", in.Lower, "open notepad")
	}
	if !reflect.DeepEqual(in.Tokens, []string{"open", "notepad"}) {
		t.Errorf("Tokens = %v, want [open notepad]", in.Tokens)
	}
	if in.Raw != "  Open  Notepad  " {
		t.Errorf("Raw not preserved")
	}
	if in.ActiveApp != "chrome.exe" {
		t.Errorf("ActiveApp = %q", in.ActiveApp)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestNormalize -v`
Expected: FAIL — package/function undefined.

- [ ] **Step 3: Implement**

Create `internal/resolver/resolver.go`:
```go
package resolver

import "strings"

// NormalizedInput is the pre-processed command handed to every Matcher.
type NormalizedInput struct {
	Raw       string   // original text, unchanged
	Lower     string   // lowercased, single-spaced, trimmed
	Tokens    []string // whitespace-split tokens of Lower
	ActiveApp string   // foreground process name, may be ""
}

// Normalize prepares raw user text for matching. activeApp may be "".
func Normalize(raw, activeApp string) NormalizedInput {
	lower := strings.ToLower(strings.TrimSpace(raw))
	tokens := strings.Fields(lower)
	return NormalizedInput{
		Raw:       raw,
		Lower:     strings.Join(tokens, " "),
		Tokens:    tokens,
		ActiveApp: activeApp,
	}
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/resolver/ -run TestNormalize -v` (expected: PASS)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add NormalizedInput and Normalize"
```

---

### Task 7: `Matcher` interface + `Resolver` chain

**Files:**
- Modify: `internal/resolver/resolver.go`
- Test: `internal/resolver/resolver_test.go`

**Interfaces:**
- Consumes: `agent.Task` from `github.com/yourname/voice-agent/internal/agent`, `NormalizedInput`.
- Produces:
  - `type Match struct{ Tasks []agent.Task; Confidence float64; Reason string }`
  - `type Matcher interface{ Name() string; Match(in NormalizedInput) (*Match, bool) }`
  - `type Resolver struct{ Matchers []Matcher; Threshold float64 }`
  - `func NewResolver(matchers ...Matcher) *Resolver` (Threshold defaults to 0.7)
  - `func (r *Resolver) Resolve(in NormalizedInput) (*Match, bool)` — first matcher whose returned `Match.Confidence >= Threshold` wins.

- [ ] **Step 1: Write the failing test**

Append to `internal/resolver/resolver_test.go`:
```go
type fakeMatcher struct {
	name string
	conf float64
	ok   bool
}

func (f fakeMatcher) Name() string { return f.name }
func (f fakeMatcher) Match(in NormalizedInput) (*Match, bool) {
	if !f.ok {
		return nil, false
	}
	return &Match{Confidence: f.conf, Reason: f.name}, true
}

func TestResolveTakesFirstAboveThreshold(t *testing.T) {
	r := NewResolver(
		fakeMatcher{"low", 0.4, true},
		fakeMatcher{"high", 0.9, true},
	)
	m, ok := r.Resolve(Normalize("x", ""))
	if !ok {
		t.Fatal("expected a match")
	}
	if m.Reason != "high" {
		t.Errorf("expected 'high' matcher to win, got %q", m.Reason)
	}
}

func TestResolveNoMatchWhenAllBelowThreshold(t *testing.T) {
	r := NewResolver(fakeMatcher{"a", 0.5, true}, fakeMatcher{"b", 0.6, true})
	if _, ok := r.Resolve(Normalize("x", "")); ok {
		t.Error("expected no match below threshold 0.7")
	}
}

func TestResolveNoMatchWhenNoneMatch(t *testing.T) {
	r := NewResolver(fakeMatcher{"a", 0.9, false})
	if _, ok := r.Resolve(Normalize("x", "")); ok {
		t.Error("expected no match when matchers decline")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestResolve -v`
Expected: FAIL — `Match`, `Matcher`, `Resolver`, `NewResolver` undefined.

- [ ] **Step 3: Implement**

Append to `internal/resolver/resolver.go`:
```go
import "github.com/yourname/voice-agent/internal/agent"

// (merge this import into the existing import block)

// DefaultThreshold is the minimum confidence for a local (Tier 0) match.
const DefaultThreshold = 0.7

// Match is a resolved local plan with a confidence score.
type Match struct {
	Tasks      []agent.Task
	Confidence float64
	Reason     string
}

// Matcher recognizes one intent domain deterministically.
type Matcher interface {
	Name() string
	Match(in NormalizedInput) (*Match, bool)
}

// Resolver runs matchers in priority order and returns the first qualifying match.
type Resolver struct {
	Matchers  []Matcher
	Threshold float64
}

func NewResolver(matchers ...Matcher) *Resolver {
	return &Resolver{Matchers: matchers, Threshold: DefaultThreshold}
}

// Resolve returns the first match whose confidence >= Threshold, else (nil,false).
func (r *Resolver) Resolve(in NormalizedInput) (*Match, bool) {
	for _, m := range r.Matchers {
		if match, ok := m.Match(in); ok && match != nil && match.Confidence >= r.Threshold {
			return match, true
		}
	}
	return nil, false
}
```

- [ ] **Step 4: Run + build + commit**

Run: `go test ./internal/resolver/ -v` (expected: PASS)
Run: `go build ./... 2>&1 | head -5` (expected: no output)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add Matcher interface and priority-chain Resolver"
```

---

## Phase C — New local action tools

### Task 8: `media_control` tool

**Files:**
- Create: `internal/tools/media_control.go`
- Modify: `internal/tools/registry.go` (register), `internal/security/permissions.go` (allow)
- Test: `internal/tools/media_control_test.go`

**Interfaces:**
- Produces: tool `media_control`, params `{"action": "play|pause|next|previous|volume_up|volume_down|mute"}`. `play`/`pause` both map to the play/pause toggle key.

- [ ] **Step 1: Write the failing test**

Create `internal/tools/media_control_test.go`:
```go
package tools

import (
	"encoding/json"
	"testing"
)

func TestMediaControlSchemaValid(t *testing.T) {
	var v map[string]interface{}
	if err := json.Unmarshal([]byte((&MediaControlTool{}).Parameters()), &v); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestMediaControlRejectsUnknownAction(t *testing.T) {
	_, err := (&MediaControlTool{}).Execute(nil, json.RawMessage(`{"action":"explode"}`))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tools/ -run TestMediaControl -v`
Expected: FAIL — `MediaControlTool` undefined.

- [ ] **Step 3: Implement**

Create `internal/tools/media_control.go`:
```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-vgo/robotgo"
)

type MediaControlTool struct{}

func (t *MediaControlTool) Name() string        { return "media_control" }
func (t *MediaControlTool) Description() string  { return "Controls system media playback and volume." }
func (t *MediaControlTool) RequiresConfirmation() bool { return false }
func (t *MediaControlTool) Parameters() string {
	return `{"type":"object","properties":{"action":{"type":"string","enum":["play","pause","next","previous","volume_up","volume_down","mute"]}},"required":["action"]}`
}

type mediaArgs struct {
	Action string `json:"action"`
}

// robotgo special keys for media/volume control (Windows).
var mediaKey = map[string]string{
	"play":        "audio_play",
	"pause":       "audio_play", // toggle
	"next":        "audio_next",
	"previous":    "audio_prev",
	"volume_up":   "audio_vol_up",
	"volume_down": "audio_vol_down",
	"mute":        "audio_mute",
}

func (t *MediaControlTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a mediaArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	key, ok := mediaKey[a.Action]
	if !ok {
		return "", fmt.Errorf("unknown media action: %q", a.Action)
	}
	robotgo.KeyTap(key)
	return fmt.Sprintf("media: %s", a.Action), nil
}
```

- [ ] **Step 4: Register + allow**

In `internal/tools/registry.go`, inside `DefaultRegistryWithConfig`, add near the other core `reg.Register(...)` calls:
```go
	reg.Register(&MediaControlTool{})
```
In `internal/security/permissions.go`, inside `DeveloperProfile`'s `AllowedTools` map, add:
```go
			"media_control": true,
```

- [ ] **Step 5: Run + build + commit**

Run: `go test ./internal/tools/ -run TestMediaControl -v` (expected: PASS)
Run: `go build ./... 2>&1 | head -5` (expected: no output)
```bash
git add internal/tools/media_control.go internal/tools/media_control_test.go internal/tools/registry.go internal/security/permissions.go
git commit -m "feat(tools): add media_control local tool"
```

---

### Task 9: `window_control` tool

**Files:**
- Create: `internal/tools/window_control.go`
- Modify: `internal/tools/registry.go`, `internal/security/permissions.go`
- Test: `internal/tools/window_control_test.go`

**Interfaces:**
- Produces: tool `window_control`, params `{"action": "minimize|maximize|close|snap_left|snap_right|switch"}`. Uses `automation.PressCombo` with robotgo key names; the Windows/Super key in robotgo is `"cmd"`.

- [ ] **Step 1: Write the failing test**

Create `internal/tools/window_control_test.go`:
```go
package tools

import (
	"encoding/json"
	"testing"
)

func TestWindowControlSchemaValid(t *testing.T) {
	var v map[string]interface{}
	if err := json.Unmarshal([]byte((&WindowControlTool{}).Parameters()), &v); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestWindowControlComboLookup(t *testing.T) {
	// Pure mapping check — does not actually send keys.
	if _, ok := windowCombo["snap_left"]; !ok {
		t.Fatal("snap_left must map to a key combo")
	}
	if _, ok := windowCombo["nope"]; ok {
		t.Fatal("unknown action must not map")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tools/ -run TestWindowControl -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/tools/window_control.go`:
```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/internal/automation"
)

type WindowControlTool struct{}

func (t *WindowControlTool) Name() string             { return "window_control" }
func (t *WindowControlTool) Description() string       { return "Manages the focused window (minimize, maximize, snap, close, switch)." }
func (t *WindowControlTool) RequiresConfirmation() bool { return false }
func (t *WindowControlTool) Parameters() string {
	return `{"type":"object","properties":{"action":{"type":"string","enum":["minimize","maximize","close","snap_left","snap_right","switch"]}},"required":["action"]}`
}

// robotgo combos; PressCombo treats the LAST element as the primary key.
// "cmd" is robotgo's name for the Windows/Super key.
var windowCombo = map[string][]string{
	"minimize":  {"cmd", "down"},
	"maximize":  {"cmd", "up"},
	"snap_left": {"cmd", "left"},
	"snap_right": {"cmd", "right"},
	"close":     {"alt", "f4"},
	"switch":    {"alt", "tab"},
}

type windowArgs struct {
	Action string `json:"action"`
}

func (t *WindowControlTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a windowArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	combo, ok := windowCombo[a.Action]
	if !ok {
		return "", fmt.Errorf("unknown window action: %q", a.Action)
	}
	if err := automation.PressCombo(combo); err != nil {
		return "", err
	}
	return fmt.Sprintf("window: %s", a.Action), nil
}
```

- [ ] **Step 4: Register + allow**

`registry.go`: `reg.Register(&WindowControlTool{})`.
`permissions.go` `DeveloperProfile`: `"window_control": true,`.

- [ ] **Step 5: Run + build + commit**

Run: `go test ./internal/tools/ -run TestWindowControl -v` (expected: PASS)
Run: `go build ./... 2>&1 | head -5`
```bash
git add internal/tools/window_control.go internal/tools/window_control_test.go internal/tools/registry.go internal/security/permissions.go
git commit -m "feat(tools): add window_control local tool"
```

---

### Task 10: `system_control` tool

**Files:**
- Create: `internal/tools/system_control.go`
- Modify: `internal/tools/registry.go`, `internal/security/permissions.go`
- Test: `internal/tools/system_control_test.go`

**Interfaces:**
- Produces: tool `system_control`, params `{"action": "lock|sleep|brightness_up|brightness_down"}`. `lock`/`sleep` use `rundll32`; brightness uses PowerShell WMI.

- [ ] **Step 1: Write the failing test**

Create `internal/tools/system_control_test.go`:
```go
package tools

import (
	"encoding/json"
	"testing"
)

func TestSystemControlSchemaValid(t *testing.T) {
	var v map[string]interface{}
	if err := json.Unmarshal([]byte((&SystemControlTool{}).Parameters()), &v); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestSystemControlRejectsUnknown(t *testing.T) {
	_, err := (&SystemControlTool{}).Execute(nil, json.RawMessage(`{"action":"selfdestruct"}`))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tools/ -run TestSystemControl -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/tools/system_control.go`:
```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type SystemControlTool struct{}

func (t *SystemControlTool) Name() string             { return "system_control" }
func (t *SystemControlTool) Description() string       { return "System actions: lock, sleep, brightness up/down." }
func (t *SystemControlTool) RequiresConfirmation() bool { return false }
func (t *SystemControlTool) Parameters() string {
	return `{"type":"object","properties":{"action":{"type":"string","enum":["lock","sleep","brightness_up","brightness_down"]}},"required":["action"]}`
}

type systemArgs struct {
	Action string `json:"action"`
}

func (t *SystemControlTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a systemArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	var cmd *exec.Cmd
	switch a.Action {
	case "lock":
		cmd = exec.Command("rundll32", "user32.dll,LockWorkStation")
	case "sleep":
		cmd = exec.Command("rundll32", "powrprof.dll,SetSuspendState", "0,1,0")
	case "brightness_up":
		cmd = brightnessCmd("+10")
	case "brightness_down":
		cmd = brightnessCmd("-10")
	default:
		return "", fmt.Errorf("unknown system action: %q", a.Action)
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	return fmt.Sprintf("system: %s", a.Action), nil
}

// brightnessCmd adjusts brightness by a signed delta via WMI.
func brightnessCmd(delta string) *exec.Cmd {
	ps := fmt.Sprintf(
		`$b=(Get-WmiObject -Namespace root/WMI -Class WmiMonitorBrightness).CurrentBrightness; `+
			`$n=[Math]::Max(0,[Math]::Min(100,$b+(%s))); `+
			`(Get-WmiObject -Namespace root/WMI -Class WmiMonitorBrightnessMethods).WmiSetBrightness(1,$n)`,
		delta,
	)
	return exec.Command("powershell", "-NoProfile", "-Command", ps)
}
```

- [ ] **Step 4: Register + allow**

`registry.go`: `reg.Register(&SystemControlTool{})`.
`permissions.go` `DeveloperProfile`: `"system_control": true,`.

- [ ] **Step 5: Run + build + commit**

Run: `go test ./internal/tools/ -run TestSystemControl -v` (expected: PASS)
Run: `go build ./... 2>&1 | head -5`
```bash
git add internal/tools/system_control.go internal/tools/system_control_test.go internal/tools/registry.go internal/security/permissions.go
git commit -m "feat(tools): add system_control local tool"
```

---

## Phase D — Matchers

> All matchers live in `internal/resolver/matchers.go`. Each task adds one matcher + tests to `internal/resolver/matchers_test.go`. Helper used by several: `taskJSON` (below, added in Task 11).

### Task 11: DateTime matcher (+ shared task helper)

**Files:**
- Modify: `internal/resolver/matchers.go` (create), `internal/resolver/matchers_test.go` (create)

**Interfaces:**
- Produces: `func taskJSON(tool string, params any) agent.Task`; `type DateTimeMatcher struct{}` implementing `Matcher`. Emits a single `get_datetime` task.

- [ ] **Step 1: Write the failing test**

Create `internal/resolver/matchers_test.go`:
```go
package resolver

import "testing"

func TestDateTimeMatcher(t *testing.T) {
	m := DateTimeMatcher{}
	for _, in := range []string{"what time is it", "what's the date", "current time"} {
		match, ok := m.Match(Normalize(in, ""))
		if !ok || match.Confidence < DefaultThreshold {
			t.Errorf("%q should match datetime, got ok=%v", in, ok)
			continue
		}
		if len(match.Tasks) != 1 || match.Tasks[0].Tool != "get_datetime" {
			t.Errorf("%q should produce get_datetime task", in)
		}
	}
	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("datetime must not match app launch")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestDateTimeMatcher -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/resolver/matchers.go`:
```go
package resolver

import (
	"encoding/json"
	"strings"

	"github.com/yourname/voice-agent/internal/agent"
)

// taskJSON builds an agent.Task, marshaling params to JSON (empty object on nil/err).
func taskJSON(tool string, params any) agent.Task {
	if params == nil {
		return agent.Task{Tool: tool, Params: json.RawMessage(`{}`)}
	}
	b, err := json.Marshal(params)
	if err != nil {
		b = []byte(`{}`)
	}
	return agent.Task{Tool: tool, Params: b}
}

// containsAny reports whether s contains any of subs.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

type DateTimeMatcher struct{}

func (DateTimeMatcher) Name() string { return "datetime" }
func (DateTimeMatcher) Match(in NormalizedInput) (*Match, bool) {
	if containsAny(in.Lower, "what time", "current time", "the time", "what's the date", "what is the date", "today's date") {
		return &Match{Tasks: []agent.Task{taskJSON("get_datetime", nil)}, Confidence: 0.95, Reason: "datetime phrase"}, true
	}
	return nil, false
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/resolver/ -run TestDateTimeMatcher -v` (expected: PASS)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add datetime matcher + task helper"
```

---

### Task 12: Web matcher (open URL + search)

**Files:**
- Modify: `internal/resolver/matchers.go`, `internal/resolver/matchers_test.go`

**Interfaces:**
- Produces: `type WebMatcher struct{}`. Emits `open_website {"url":...}` for domains/URLs, else `web_search {"query":...}` for "search/google …".

- [ ] **Step 1: Write the failing test**

Append to `internal/resolver/matchers_test.go`:
```go
func TestWebMatcher(t *testing.T) {
	m := WebMatcher{}

	match, ok := m.Match(Normalize("open youtube.com", ""))
	if !ok || len(match.Tasks) != 1 || match.Tasks[0].Tool != "open_website" {
		t.Fatalf("expected open_website for a domain, ok=%v", ok)
	}
	if !containsString(string(match.Tasks[0].Params), "youtube.com") {
		t.Errorf("url param missing domain: %s", match.Tasks[0].Params)
	}

	match, ok = m.Match(Normalize("google golang generics", ""))
	if !ok || match.Tasks[0].Tool != "web_search" {
		t.Fatalf("expected web_search, ok=%v", ok)
	}

	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("web matcher must not claim a bare app name")
	}
}

func containsString(s, sub string) bool { return len(s) >= len(sub) && (func() bool { return indexOf(s, sub) >= 0 })() }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestWebMatcher -v`
Expected: FAIL — `WebMatcher` undefined.

- [ ] **Step 3: Implement**

Append to `internal/resolver/matchers.go`:
```go
import "regexp" // merge into the existing import block

// domainRe matches a bare domain or URL like "youtube.com" or "https://x.io/y".
var domainRe = regexp.MustCompile(`\b([a-z0-9-]+\.(com|org|net|io|dev|ai|co|gov|edu))(/\S*)?\b`)

type WebMatcher struct{}

func (WebMatcher) Name() string { return "web" }
func (WebMatcher) Match(in NormalizedInput) (*Match, bool) {
	// 1) explicit URL/domain anywhere in the input -> open_website
	if loc := domainRe.FindString(in.Lower); loc != "" {
		url := loc
		if !strings.HasPrefix(url, "http") {
			url = "https://" + url
		}
		return &Match{
			Tasks:      []agent.Task{taskJSON("open_website", map[string]string{"url": url})},
			Confidence: 0.9, Reason: "domain detected",
		}, true
	}
	// 2) "search X" / "google X" -> web_search
	for _, verb := range []string{"search ", "google ", "look up "} {
		if strings.HasPrefix(in.Lower, verb) {
			q := strings.TrimSpace(strings.TrimPrefix(in.Lower, verb))
			if q == "" {
				return nil, false
			}
			return &Match{
				Tasks:      []agent.Task{taskJSON("web_search", map[string]string{"query": q})},
				Confidence: 0.85, Reason: "search verb",
			}, true
		}
	}
	return nil, false
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/resolver/ -run TestWebMatcher -v` (expected: PASS)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add web matcher (open url + search)"
```

---

### Task 13: AppLauncher matcher (fuzzy, ambiguity-aware)

**Files:**
- Modify: `internal/resolver/matchers.go`, `internal/resolver/matchers_test.go`

**Interfaces:**
- Produces: `type AppMatcher struct{ Lookup func(query string) (name string, count int) }`. The injectable `Lookup` returns the best app name and how many apps matched (for ambiguity + testability). Emits `open_app {"app_name":...}`. Default lookup wraps `executor.FindApp` + a count.

- [ ] **Step 1: Write the failing test**

Append to `internal/resolver/matchers_test.go`:
```go
func TestAppMatcher(t *testing.T) {
	m := AppMatcher{Lookup: func(q string) (string, int) {
		if q == "notepad" {
			return "Notepad", 1
		}
		if q == "word" {
			return "Word", 3 // ambiguous
		}
		return "", 0
	}}

	match, ok := m.Match(Normalize("open notepad", ""))
	if !ok || match.Tasks[0].Tool != "open_app" {
		t.Fatalf("expected open_app, ok=%v", ok)
	}
	if match.Confidence < DefaultThreshold {
		t.Errorf("single strong match should be >= threshold, got %v", match.Confidence)
	}

	// ambiguous -> confidence must drop below threshold so it falls to Tier 1
	amb, ok := m.Match(Normalize("open word", ""))
	if ok && amb.Confidence >= DefaultThreshold {
		t.Errorf("ambiguous app match should be < threshold, got %v", amb.Confidence)
	}

	// no launch verb -> no match
	if _, ok := m.Match(Normalize("what time is it", "")); ok {
		t.Error("app matcher requires a launch verb")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestAppMatcher -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Append to `internal/resolver/matchers.go`:
```go
type AppMatcher struct {
	// Lookup returns the best-matching app display name and the number of apps matched.
	Lookup func(query string) (name string, count int)
}

var appLaunchVerbs = []string{"open ", "launch ", "start ", "run "}

func (a AppMatcher) Name() string { return "app" }
func (a AppMatcher) Match(in NormalizedInput) (*Match, bool) {
	var query string
	for _, v := range appLaunchVerbs {
		if strings.HasPrefix(in.Lower, v) {
			query = strings.TrimSpace(strings.TrimPrefix(in.Lower, v))
			break
		}
	}
	if query == "" || a.Lookup == nil {
		return nil, false
	}
	name, count := a.Lookup(query)
	if count == 0 || name == "" {
		return nil, false
	}
	conf := 0.9
	if count > 1 {
		conf = 0.5 // ambiguous -> below threshold, falls to Tier 1 / disambiguation
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("open_app", map[string]string{"app_name": name})},
		Confidence: conf, Reason: "app launch verb",
	}, true
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/resolver/ -run TestAppMatcher -v` (expected: PASS)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add ambiguity-aware app launcher matcher"
```

---

### Task 14: FileFind matcher

**Files:**
- Modify: `internal/resolver/matchers.go`, `internal/resolver/matchers_test.go`

**Interfaces:**
- Produces: `type FileMatcher struct{ Search func(query string) []string }`. `Search` returns candidate absolute paths. Emits `open_file {"file_path":...}` when exactly one strong candidate; ambiguous (>1) drops confidence. Default `Search` wraps `search.SearchFiles` mapping to `.Path`.

- [ ] **Step 1: Write the failing test**

Append to `internal/resolver/matchers_test.go`:
```go
func TestFileMatcher(t *testing.T) {
	m := FileMatcher{Search: func(q string) []string {
		if q == "resume.pdf" {
			return []string{`C:\Users\me\resume.pdf`}
		}
		if q == "report" {
			return []string{`C:\a\report.docx`, `C:\b\report.xlsx`}
		}
		return nil
	}}

	match, ok := m.Match(Normalize("open file resume.pdf", ""))
	if !ok || match.Tasks[0].Tool != "open_file" {
		t.Fatalf("expected open_file, ok=%v", ok)
	}
	if !containsString(string(match.Tasks[0].Params), "resume.pdf") {
		t.Errorf("file_path missing: %s", match.Tasks[0].Params)
	}

	amb, ok := m.Match(Normalize("open file report", ""))
	if ok && amb.Confidence >= DefaultThreshold {
		t.Errorf("multiple file hits should be < threshold, got %v", amb.Confidence)
	}

	if _, ok := m.Match(Normalize("open file nothinghere", "")); ok {
		t.Error("no hits -> no match")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestFileMatcher -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Append to `internal/resolver/matchers.go`:
```go
type FileMatcher struct {
	Search func(query string) []string // returns candidate absolute paths
}

func (f FileMatcher) Name() string { return "file" }
func (f FileMatcher) Match(in NormalizedInput) (*Match, bool) {
	// require an explicit "file" cue to avoid stealing app launches
	if !strings.HasPrefix(in.Lower, "open file ") && !strings.HasPrefix(in.Lower, "find file ") {
		return nil, false
	}
	query := strings.TrimSpace(in.Lower)
	query = strings.TrimPrefix(query, "open file ")
	query = strings.TrimPrefix(query, "find file ")
	if query == "" || f.Search == nil {
		return nil, false
	}
	hits := f.Search(query)
	if len(hits) == 0 {
		return nil, false
	}
	conf := 0.85
	if len(hits) > 1 {
		conf = 0.5
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("open_file", map[string]string{"file_path": hits[0]})},
		Confidence: conf, Reason: "file cue + index hit",
	}, true
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/resolver/ -run TestFileMatcher -v` (expected: PASS)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add file find matcher"
```

---

### Task 15: MediaControl matcher

**Files:**
- Modify: `internal/resolver/matchers.go`, `internal/resolver/matchers_test.go`

**Interfaces:**
- Produces: `type MediaMatcher struct{}`. Emits `media_control {"action":...}`.

- [ ] **Step 1: Write the failing test**

Append to `internal/resolver/matchers_test.go`:
```go
func TestMediaMatcher(t *testing.T) {
	m := MediaMatcher{}
	cases := map[string]string{
		"pause":          "pause",
		"pause music":    "pause",
		"play":           "play",
		"next track":     "next",
		"previous song":  "previous",
		"volume up":      "volume_up",
		"volume down":    "volume_down",
		"mute":           "mute",
	}
	for phrase, want := range cases {
		match, ok := m.Match(Normalize(phrase, ""))
		if !ok {
			t.Errorf("%q should match media", phrase)
			continue
		}
		if !containsString(string(match.Tasks[0].Params), want) {
			t.Errorf("%q -> want action %q, params=%s", phrase, want, match.Tasks[0].Params)
		}
	}
	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("media must not match app launch")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestMediaMatcher -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Append to `internal/resolver/matchers.go`:
```go
type MediaMatcher struct{}

func (MediaMatcher) Name() string { return "media" }
func (MediaMatcher) Match(in NormalizedInput) (*Match, bool) {
	l := in.Lower
	var action string
	switch {
	case containsAny(l, "volume up", "louder", "turn it up"):
		action = "volume_up"
	case containsAny(l, "volume down", "quieter", "turn it down"):
		action = "volume_down"
	case containsAny(l, "mute", "unmute"):
		action = "mute"
	case containsAny(l, "next track", "next song", "skip"):
		action = "next"
	case containsAny(l, "previous track", "previous song", "go back a track"):
		action = "previous"
	case containsAny(l, "pause"):
		action = "pause"
	case l == "play" || containsAny(l, "play music", "resume music", "resume playback"):
		action = "play"
	default:
		return nil, false
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("media_control", map[string]string{"action": action})},
		Confidence: 0.9, Reason: "media phrase",
	}, true
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/resolver/ -run TestMediaMatcher -v` (expected: PASS)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add media control matcher"
```

---

### Task 16: SystemToggle matcher

**Files:**
- Modify: `internal/resolver/matchers.go`, `internal/resolver/matchers_test.go`

**Interfaces:**
- Produces: `type SystemMatcher struct{}`. Emits `system_control {"action":...}`.

- [ ] **Step 1: Write the failing test**

Append to `internal/resolver/matchers_test.go`:
```go
func TestSystemMatcher(t *testing.T) {
	m := SystemMatcher{}
	cases := map[string]string{
		"lock the pc":     "lock",
		"lock computer":   "lock",
		"go to sleep":     "sleep",
		"brightness up":   "brightness_up",
		"brightness down": "brightness_down",
	}
	for phrase, want := range cases {
		match, ok := m.Match(Normalize(phrase, ""))
		if !ok || !containsString(string(match.Tasks[0].Params), want) {
			t.Errorf("%q -> want %q, ok=%v", phrase, want, ok)
		}
	}
	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("system must not match app launch")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestSystemMatcher -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Append to `internal/resolver/matchers.go`:
```go
type SystemMatcher struct{}

func (SystemMatcher) Name() string { return "system" }
func (SystemMatcher) Match(in NormalizedInput) (*Match, bool) {
	l := in.Lower
	var action string
	switch {
	case containsAny(l, "lock the pc", "lock computer", "lock screen", "lock my"):
		action = "lock"
	case containsAny(l, "go to sleep", "sleep the pc", "put to sleep", "suspend"):
		action = "sleep"
	case containsAny(l, "brightness up", "brighter", "increase brightness"):
		action = "brightness_up"
	case containsAny(l, "brightness down", "dimmer", "decrease brightness", "lower brightness"):
		action = "brightness_down"
	default:
		return nil, false
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("system_control", map[string]string{"action": action})},
		Confidence: 0.9, Reason: "system phrase",
	}, true
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/resolver/ -run TestSystemMatcher -v` (expected: PASS)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add system toggle matcher"
```

---

### Task 17: WindowControl matcher

**Files:**
- Modify: `internal/resolver/matchers.go`, `internal/resolver/matchers_test.go`

**Interfaces:**
- Produces: `type WindowMatcher struct{}`. Emits `window_control {"action":...}`.

- [ ] **Step 1: Write the failing test**

Append to `internal/resolver/matchers_test.go`:
```go
func TestWindowMatcher(t *testing.T) {
	m := WindowMatcher{}
	cases := map[string]string{
		"minimize window":  "minimize",
		"maximize window":  "maximize",
		"snap left":        "snap_left",
		"snap right":       "snap_right",
		"close window":     "close",
		"switch window":    "switch",
	}
	for phrase, want := range cases {
		match, ok := m.Match(Normalize(phrase, ""))
		if !ok || !containsString(string(match.Tasks[0].Params), want) {
			t.Errorf("%q -> want %q, ok=%v", phrase, want, ok)
		}
	}
	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("window must not match app launch")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestWindowMatcher -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Append to `internal/resolver/matchers.go`:
```go
type WindowMatcher struct{}

func (WindowMatcher) Name() string { return "window" }
func (WindowMatcher) Match(in NormalizedInput) (*Match, bool) {
	l := in.Lower
	var action string
	switch {
	case containsAny(l, "snap left", "dock left"):
		action = "snap_left"
	case containsAny(l, "snap right", "dock right"):
		action = "snap_right"
	case containsAny(l, "minimize", "minimise"):
		action = "minimize"
	case containsAny(l, "maximize", "maximise", "full screen this"):
		action = "maximize"
	case containsAny(l, "close window", "close this window"):
		action = "close"
	case containsAny(l, "switch window", "switch app", "alt tab"):
		action = "switch"
	default:
		return nil, false
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("window_control", map[string]string{"action": action})},
		Confidence: 0.9, Reason: "window phrase",
	}, true
}
```

- [ ] **Step 4: Run + commit**

Run: `go test ./internal/resolver/ -run TestWindowMatcher -v` (expected: PASS)
```bash
git add internal/resolver/
git commit -m "feat(resolver): add window control matcher"
```

---

### Task 18: `Default()` resolver wiring (priority order)

**Files:**
- Modify: `internal/resolver/matchers.go`, `internal/resolver/matchers_test.go`

**Interfaces:**
- Produces: `func Default() *Resolver` — wires all seven matchers with production `Lookup`/`Search` closures, in priority order: datetime, media, system, window, web, file, app. (App last because its launch verbs overlap; specific-phrase matchers win first.)

- [ ] **Step 1: Write the failing test**

Append to `internal/resolver/matchers_test.go`:
```go
func TestDefaultResolverPriority(t *testing.T) {
	r := Default()
	// "pause" must resolve to media, not fall through
	if m, ok := r.Resolve(Normalize("pause", "")); !ok || m.Tasks[0].Tool != "media_control" {
		t.Errorf("'pause' should resolve to media_control")
	}
	// a domain resolves to open_website
	if m, ok := r.Resolve(Normalize("open github.com", "")); !ok || m.Tasks[0].Tool != "open_website" {
		t.Errorf("'open github.com' should resolve to open_website")
	}
	// gibberish falls through (no local match)
	if _, ok := r.Resolve(Normalize("ponder the meaning of life", "")); ok {
		t.Errorf("open-ended request must fall through to Tier 1")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/resolver/ -run TestDefaultResolverPriority -v`
Expected: FAIL — `Default` undefined.

- [ ] **Step 3: Implement**

Append to `internal/resolver/matchers.go`:
```go
import (
	"github.com/yourname/voice-agent/internal/executor" // merge into import block
	"github.com/yourname/voice-agent/internal/search"   // merge into import block
)

// Default wires all seven matchers backed by real OS/index lookups, in priority order.
func Default() *Resolver {
	appLookup := func(q string) (string, int) {
		app, found := executor.FindApp(q)
		if !found {
			return "", 0
		}
		// executor.FindApp returns a single best match; treat as unambiguous.
		// (A future refinement can return a real candidate count.)
		return app.Name, 1
	}
	fileSearch := func(q string) []string {
		recs := search.SearchFiles(q)
		paths := make([]string, 0, len(recs))
		for _, r := range recs {
			paths = append(paths, r.Path)
		}
		return paths
	}
	return NewResolver(
		DateTimeMatcher{},
		MediaMatcher{},
		SystemMatcher{},
		WindowMatcher{},
		WebMatcher{},
		FileMatcher{Search: fileSearch},
		AppMatcher{Lookup: appLookup},
	)
}
```

- [ ] **Step 4: Run + build + commit**

Run: `go test ./internal/resolver/ -v 2>&1 | tail -20` (expected: PASS)
Run: `go build ./... 2>&1 | head -5` (expected: no output)
```bash
git add internal/resolver/
git commit -m "feat(resolver): wire Default() resolver with all matchers in priority order"
```

---

## Phase E — Dispatch integration

### Task 19: Central dispatch + consolidated security

**Files:**
- Create: `internal/dispatch/dispatch.go`
- Test: `internal/dispatch/dispatch_test.go`

**Interfaces:**
- Consumes: `resolver.Resolver`, `tools.Registry`, `llm.Provider`, `security.Profile`, `agent.NewExecutor`, `agent.NewOrchestrator`.
- Produces:
  - `type Deps struct{ Registry *tools.Registry; Provider llm.Provider; Profile *security.Profile; Resolver *resolver.Resolver }`
  - `func (d *Deps) Handle(ctx context.Context, input, activeApp string) error` — Tier 0 on match, Tier 1 otherwise.
  - `func (d *Deps) enforceSecurity(tasks []agent.Task) error`
  - tier counters: `func LocalCount() int64`, `func CloudCount() int64`.

- [ ] **Step 1: Write the failing test**

Create `internal/dispatch/dispatch_test.go`:
```go
package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yourname/voice-agent/internal/agent"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/resolver"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
)

// recordingProvider fails the test if any provider method is called.
type recordingProvider struct{ called bool }

func (p *recordingProvider) GenerateIntent(context.Context, llm.IntentRequest) (llm.IntentResponse, error) {
	p.called = true
	return llm.IntentResponse{}, nil
}
func (p *recordingProvider) StreamGenerateIntent(context.Context, llm.IntentRequest, chan<- string) (llm.IntentResponse, error) {
	p.called = true
	return llm.IntentResponse{}, nil
}
func (p *recordingProvider) ClassifyAndPlan(context.Context, string, string, string) (llm.ClassifyResponse, error) {
	p.called = true
	return llm.ClassifyResponse{}, nil
}
func (p *recordingProvider) Generate(context.Context, string, [][]byte) (string, error) {
	p.called = true
	return "[]", nil
}

// staticMatcher always returns a get_datetime task above threshold.
type staticMatcher struct{}

func (staticMatcher) Name() string { return "static" }
func (staticMatcher) Match(in resolver.NormalizedInput) (*resolver.Match, bool) {
	return &resolver.Match{
		Tasks:      []agent.Task{{Tool: "get_datetime", Params: json.RawMessage(`{}`)}},
		Confidence: 1.0,
	}, true
}

func TestHandleTier0MakesNoProviderCall(t *testing.T) {
	prov := &recordingProvider{}
	reg := tools.DefaultRegistry(prov) // includes get_datetime
	profile := security.DeveloperProfile()
	d := &Deps{
		Registry: reg,
		Provider: prov,
		Profile:  &profile,
		Resolver: resolver.NewResolver(staticMatcher{}),
	}
	if err := d.Handle(context.Background(), "what time is it", ""); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if prov.called {
		t.Fatal("Tier 0 path must not call the LLM provider")
	}
	if LocalCount() < 1 {
		t.Error("LocalCount should have incremented")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/dispatch/ -run TestHandleTier0 -v`
Expected: FAIL — package/`Deps` undefined.

- [ ] **Step 3: Implement**

Create `internal/dispatch/dispatch.go`:
```go
package dispatch

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/yourname/voice-agent/internal/agent"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/resolver"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
)

// Deps holds everything the tiered dispatcher needs. Construct once in main.
type Deps struct {
	Registry *tools.Registry
	Provider llm.Provider
	Profile  *security.Profile
	Resolver *resolver.Resolver
}

var localHits, cloudHits int64

func LocalCount() int64 { return atomic.LoadInt64(&localHits) }
func CloudCount() int64 { return atomic.LoadInt64(&cloudHits) }

// Handle routes one command through Tier 0 (local) or Tier 1 (cloud).
// activeApp is the foreground process name (may be "").
func (d *Deps) Handle(ctx context.Context, input, activeApp string) error {
	norm := resolver.Normalize(input, activeApp)
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
	log.Printf("[dispatch] TIER1 (cloud) %q", input)
	exec := agent.NewExecutor(d.Registry)
	orch := agent.NewOrchestrator(d.Provider, exec)
	return orch.Run(ctx, input)
}

// enforceSecurity applies the profile allow-list and per-tool confirmation once.
func (d *Deps) enforceSecurity(tasks []agent.Task) error {
	for _, task := range tasks {
		if d.Profile != nil && !d.Profile.IsAllowed(task.Tool) {
			return fmt.Errorf("tool %q not permitted in profile %q", task.Tool, d.Profile.Name)
		}
		tool, found := d.Registry.Get(task.Tool)
		if !found {
			return fmt.Errorf("tool %q not registered", task.Tool)
		}
		if tool.RequiresConfirmation() {
			if !security.RequestConfirmation(task.Tool, task.Params) {
				return fmt.Errorf("action %q cancelled by user", task.Tool)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run + build + commit**

Run: `go test ./internal/dispatch/ -v` (expected: PASS)
Run: `go build ./... 2>&1 | head -5` (expected: no output)
```bash
git add internal/dispatch/
git commit -m "feat(dispatch): tiered Handle with consolidated security + tier counters"
```

---

### Task 20: Wire the text path through dispatch

**Files:**
- Modify: `internal/command/router.go`, `cmd/app/main.go`

**Interfaces:**
- Consumes: `dispatch.Deps`. The router holds a package-level `*dispatch.Deps` set in `InitRouter`.

- [ ] **Step 1: Extend `InitRouter` to build dispatch deps**

In `internal/command/router.go`, add to the `var (...)` block:
```go
	globalDispatch *dispatch.Deps
```
Add the import `"github.com/yourname/voice-agent/internal/dispatch"` and `"github.com/yourname/voice-agent/internal/resolver"`. In `InitRouter`, after assigning the globals, add:
```go
	globalDispatch = &dispatch.Deps{
		Registry: registry,
		Provider: provider,
		Profile:  profile,
		Resolver: resolver.Default(),
	}
```

- [ ] **Step 2: Replace the deterministic branch in `ProcessCommand`**

In `ProcessCommand`, keep the `ai ` prefix branch. Replace the `Parse`→`CreatePlan`→`validatePlan`→`ExecutePlan` block (everything after the `ai ` check) with:
```go
	sysCtx := agentctx.BuildContext()
	activeApp := ""
	if sysCtx.Window != nil {
		activeApp = sysCtx.Window.ProcessName
	}
	if err := globalDispatch.Handle(globalCtx, input, activeApp); err != nil {
		log.Printf("dispatch failed: %v", err)
	}
```
Add import `agentctx "github.com/yourname/voice-agent/internal/context"` if not present. Remove the now-unused `Parse`/`agent.CreatePlan`/`validatePlan` references from this function only. Leave `RunAICommand`, `validatePlan`, `isAllowed` intact (still used by the `ai ` path).

- [ ] **Step 3: Build + smoke test**

Run: `go build ./... 2>&1 | head -10` (expected: no output; if `Parse` becomes unused package-wide, keep it — it's still referenced by tests/other paths; only remove imports the compiler flags).

- [ ] **Step 4: Commit**

```bash
git add internal/command/router.go
git commit -m "feat(command): route typed commands through tiered dispatch"
```

---

### Task 21: Wire the voice path through dispatch

**Files:**
- Modify: `internal/engine/runtime.go`

**Interfaces:**
- Consumes: `dispatch.Deps`. The `Engine` struct gains a `Dispatch *dispatch.Deps` field, populated in `NewEngine`.

- [ ] **Step 1: Add a Dispatch field to Engine**

In `internal/engine/runtime.go`, add to the `Engine` struct:
```go
	Dispatch *dispatch.Deps
```
Add import `"github.com/yourname/voice-agent/internal/dispatch"` and `"github.com/yourname/voice-agent/internal/resolver"`. In `NewEngine(...)`, set:
```go
	e.Dispatch = &dispatch.Deps{
		Registry: registry,
		Provider: provider,
		Profile:  profile,
		Resolver: resolver.Default(),
	}
```
(Match `NewEngine`'s actual parameter names for registry/provider/profile.)

- [ ] **Step 2: Replace the `EventTranscribed` orchestrator block**

In `handleEvent`, replace the `EventTranscribed` goroutine body that builds `agent.NewOrchestrator(...)` and calls `orch.Run` with:
```go
		go func() {
			ui.SetState(ui.StateExecuting)
			activeApp := ""
			if c := agentctx.BuildContext(); c != nil && c.Window != nil {
				activeApp = c.Window.ProcessName
			}
			if err := e.Dispatch.Handle(ctx, transcript, activeApp); err != nil {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("dispatch failed: %w", err)}
				audit.LogAction(transcript, "dispatch", nil, "FAILED: "+err.Error())
				return
			}
			audit.LogAction(transcript, "dispatch", nil, "SUCCESS")
			e.Events <- Event{Type: EventToolExecuted, Payload: agent.Plan{Transcript: transcript, Intent: "dispatch"}}
		}()
```
Ensure `agentctx "github.com/yourname/voice-agent/internal/context"` is imported (it was used by the deleted `planExecution`; re-add if the compiler flags it).

- [ ] **Step 3: Build**

Run: `go build ./... 2>&1 | head -10` (expected: no output).

- [ ] **Step 4: Commit**

```bash
git add internal/engine/runtime.go
git commit -m "feat(engine): route voice transcripts through tiered dispatch"
```

---

## Phase F — Overhead lightening

### Task 22: Lazy-init the automation & highlight overlays

**Files:**
- Modify: `internal/ui/automation_overlay.go`, `internal/ui/highlight_overlay.go`, `cmd/app/main.go`

**Interfaces:**
- Produces: `ShowAutomationStep`/`FlashHighlightBox` transparently create their WebView on first call via `sync.Once`, so `main.go` no longer launches them at startup.

- [ ] **Step 1: Guard the automation overlay behind sync.Once**

In `internal/ui/automation_overlay.go`, add a package-level `var autoOnce sync.Once`. Wrap the body of `RunAutomationOverlay()` so its window creation runs once. Change `ShowAutomationStep(...)` to call `ensureAutomationOverlay()` first:
```go
func ensureAutomationOverlay() {
	autoOnce.Do(func() { go RunAutomationOverlay() })
}
```
Call `ensureAutomationOverlay()` at the top of `ShowAutomationStep`. Add `"sync"` to imports.

- [ ] **Step 2: Same for the highlight overlay**

In `internal/ui/highlight_overlay.go`, add `var hlOnce sync.Once` and `ensureHighlightOverlay()` that starts `RunHighlightOverlay()` once; call it at the top of `FlashHighlightBox`.

- [ ] **Step 3: Remove the eager launches in main.go**

In `cmd/app/main.go`, delete the two startup goroutines `go ui.RunAutomationOverlay()` and `go ui.RunHighlightOverlay()` (they now start on first use).

- [ ] **Step 4: Build + commit**

Run: `go build ./... 2>&1 | head -5` (expected: no output)
```bash
git add internal/ui/automation_overlay.go internal/ui/highlight_overlay.go cmd/app/main.go
git commit -m "perf(ui): lazy-init automation & highlight overlays on first use"
```

---

### Task 23: Gate voice/wake-word init on config

**Files:**
- Modify: `cmd/app/main.go`

**Interfaces:**
- Consumes: `cfg.EnableVoice` (bool, already in config), `cfg.PorcupineAccessKey` (string).

- [ ] **Step 1: Guard Whisper init**

In `cmd/app/main.go`, wrap the `asr.SetPaths(cfg.WhisperPath, cfg.WhisperModel)` call so it only runs when voice is enabled:
```go
	if cfg.EnableVoice {
		asr.SetPaths(cfg.WhisperPath, cfg.WhisperModel)
	} else {
		log.Println("Voice disabled (enable_voice=false); skipping Whisper init.")
	}
```

- [ ] **Step 2: Guard wake-word init**

If `main.go` starts Porcupine (`wakeword.ListenForWakeWord`), wrap it in `if cfg.EnableVoice && cfg.PorcupineAccessKey != "" { ... }`. If no such call exists yet, skip this step.

- [ ] **Step 3: Build + commit**

Run: `go build ./... 2>&1 | head -5` (expected: no output)
```bash
git add cmd/app/main.go
git commit -m "perf: skip whisper/wakeword init when voice is disabled"
```

---

### Task 24: Strip binary + document build/architecture

**Files:**
- Modify: `README.md`, `CLAUDE.md`, `docs/superpowers/plans/sp1-baseline.md`

- [ ] **Step 1: Build stripped and record the new size**

Run:
```bash
cd "E:/Voice Agent"
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
ls -la voice-agent.exe | awk '{print $5}'
```
Record the new size.

- [ ] **Step 2: Fill the baseline "after" column**

Open `docs/superpowers/plans/sp1-baseline.md`, fill the "after" column for binary size. Relaunch the binary and record idle WorkingSet64 + cold-start again for the "after" column (per Task 1 steps 1–2).

- [ ] **Step 3: Update build docs**

In `README.md` and `CLAUDE.md`, change the documented build command to:
```bash
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
```
Add a short "Tiered dispatch (SP1)" note under Architecture in both, describing Tier 0 (local resolver) → Tier 1 (cloud) with the `internal/resolver` + `internal/dispatch` pointers.

- [ ] **Step 4: Commit**

```bash
git add README.md CLAUDE.md docs/superpowers/plans/sp1-baseline.md
git commit -m "docs(sp1): stripped build flags, tiered-dispatch architecture note, filled baseline"
```

---

### Task 25: Full-suite verification

**Files:** none (verification only)

- [ ] **Step 1: Vet + full test suite**

Run:
```bash
cd "E:/Voice Agent"
go vet ./... 2>&1 | head -20
go test ./... 2>&1 | tail -30
```
Expected: vet clean; all tests PASS (whisper.cpp binding package may be skipped/irrelevant — note any pre-existing unrelated failures rather than fixing them here).

- [ ] **Step 2: Manual smoke (Tier 0, zero network)**

Launch `./voice-agent.exe`, open the command bar (Ctrl+Space), type `open notepad` → Notepad launches instantly. Type `pause` → media toggles. Type `what time is it`. Confirm the logs show `[dispatch] TIER0` for each. Type a reasoning request (`ai summarize my clipboard` or `write me a haiku`) → confirm `[dispatch] TIER1`.

- [ ] **Step 3: Confirm the DoD criteria**

Verify against the spec §1: local commands hit Tier 0 (log), no provider call for them (Task 19 test proves it), binary/RAM numbers recorded in `sp1-baseline.md`. Note any criterion not met.

- [ ] **Step 4: Final commit (if any doc updates)**

```bash
git add -A
git commit -m "chore(sp1): full-suite verification pass" || echo "nothing to commit"
```

---

## Self-Review (completed by plan author)

- **Spec coverage:** §2.1 resolver core → Tasks 6–7; §2.2 seven matchers → Tasks 11–18 (+ new tools 8–10); §2.3 unified dispatch + consolidated security → Tasks 19–21; §2.4 tier-usage log → Task 19 counters; §3.1 delete cruft → Tasks 2–3; §3.2 bug fixes → Tasks 4–5; §3.3 dormant marking → Task 3 step 4; §3.4 lighten → Tasks 22–24; §4 testing → per-task TDD + Task 25; §1 baseline numbers → Tasks 1 & 24. No uncovered spec sections.
- **Placeholder scan:** no TBD/TODO; every code step contains real code. Two intentional "read the actual signature and adjust" notes (classify prompt `%s` arity in Task 4; `NewEngine` param names in Task 21) are guardrails, not placeholders.
- **Type consistency:** `Match`, `Matcher`, `NormalizedInput`, `Resolver`, `taskJSON`, `Deps.Handle`, tool names (`media_control`/`window_control`/`system_control`) and their param keys are used identically across producing and consuming tasks.
```
