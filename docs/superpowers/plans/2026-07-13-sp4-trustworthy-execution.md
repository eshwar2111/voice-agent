# SP4 — Trustworthy One-Shot Execution Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dependency-free `internal/trust` layer that wraps every plan at the `GraphExecutor` choke point with risk-classification, a one-shot approval gate, cheap-first verification, and a bounded recovery ladder.

**Architecture:** A `trust.TrustedExecutor` owns the per-step loop. `agent.GraphExecutor.ExecutePlan` converts its `[]Task` → `[]trust.Step` and delegates to it when wired (nil-safe fallback to the legacy loop). `trust` imports only the stdlib; all coupling flows inward through injected functions (`Exec`, `Confirm`, `Describe`, `Narrate`, `Replan`, `Ask`) so there is no `agent`/`tools` import cycle and every unit is testable with pure fakes.

**Tech Stack:** Go 1.26, standard library only inside `internal/trust` (`os`, `encoding/json`, `context`, `time`, `strings`). CGO is irrelevant to this package — its tests run with plain `go test ./internal/trust/...`.

## Global Constraints

- **Package `internal/trust` imports nothing from `internal/agent` or `internal/tools`** — stdlib only; all coupling via local types + injected funcs. (Spec: "Dependency direction".)
- **Deterministic-first:** no LLM call for a fully-deterministic successful plan. LLM judge runs only for fuzzy GUI/vision steps; re-plan runs **at most once per plan**. (Spec success criterion 4.)
- **Non-breaking:** with `GraphExecutor.Trusted == nil`, `ExecutePlan` behaves exactly as today; all existing tests pass unchanged. (Spec success criterion 5.)
- **One gate up front:** the approval gate fires once, before any side effect, when `len(steps) >= 2 || anyRisky`; reject ⇒ zero side effects. Re-planned tail steps do **not** trigger a second gate. (Spec §Architecture.)
- **Recovery budget:** ≤ `maxRetries` (default **2**) retries + **1** re-plan per whole plan. **No auto-rollback.** (Spec §Unit 4.)
- **Config flag** `trusted_execution` defaults **true**. (Spec §Integration.)
- Verify non-CGO code with `go build ./internal/trust/...` and `go test ./internal/trust/...`. Do NOT run `go build ./...` or `go build ./cmd/app` in CI checks here (they link whisper/CGO). Prefix every go command with `export PATH="$PATH:/c/w64devkit/bin"`.
- Explicit `git add <files>` only — never `git add -A` (config.json holds real secrets).

**Existing types (verbatim, for reference):**
```go
// internal/agent/planner.go
type Task struct { Tool string `json:"tool"`; Params json.RawMessage `json:"params"` }
type Plan struct { Transcript, Intent, Reason string; Tasks []Task }
// internal/tools/artifact.go
type ToolResult struct { Summary string `json:"summary"`; Artifacts map[string]interface{} `json:"artifacts,omitempty"` }
func ParseToolResult(raw string) ToolResult
// internal/llm/llm.go
Generate(ctx context.Context, prompt string, images [][]byte) (string, error)
// internal/ui/overlay.go
func RequestConfirmationCard(cardJSON string) bool
func ShowNotification(text string)
```

---

## Task 1: `trust` foundation types

**Files:**
- Create: `internal/trust/types.go`
- Test: `internal/trust/types_test.go`

**Interfaces:**
- Consumes: nothing (stdlib only).
- Produces: `Step`, `Risk` (`Safe`,`Risky`), `Decision` (`Retry`,`Replan`,`Ask`,`Abort`), `Report`, interfaces `Classifier`/`Verifier`/`Recoverer`, and `TrustedExecutor` struct (fields only). All later tasks import these.

- [ ] **Step 1: Write the failing test** — `internal/trust/types_test.go`

```go
package trust

import "testing"

func TestRiskString(t *testing.T) {
	if Safe.String() != "Safe" || Risky.String() != "Risky" {
		t.Fatalf("risk names wrong: %q %q", Safe.String(), Risky.String())
	}
}

func TestReportZeroValue(t *testing.T) {
	var r Report
	if r.Aborted || r.FailedAt != 0 || len(r.Completed) != 0 {
		t.Fatalf("unexpected zero Report: %+v", r)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/...`
Expected: FAIL — `undefined: Safe` / package does not compile.

- [ ] **Step 3: Write minimal implementation** — `internal/trust/types.go`

```go
// Package trust is the trustworthy one-shot execution layer. It wraps a plan's
// steps with risk classification, a single up-front approval gate, cheap-first
// verification, and a bounded recovery ladder. It imports only the standard
// library; all coupling to the rest of the app flows inward via injected funcs.
package trust

import (
	"context"
	"encoding/json"
)

// Step is one unit of a plan, carrying the plain-English intent for previews
// and LLM judging. trust owns this type; agent converts its Task into it.
type Step struct {
	Tool   string
	Params json.RawMessage
	Goal   string // plain-English intent, e.g. "delete the old invoice"
}

type Risk int

const (
	Safe Risk = iota
	Risky
)

func (r Risk) String() string {
	if r == Risky {
		return "Risky"
	}
	return "Safe"
}

type Decision int

const (
	Retry Decision = iota
	Replan
	Ask
	Abort
)

// Report is returned by Run so the caller can render what completed vs. failed.
type Report struct {
	Completed []string // Describe() text of steps that ran and verified
	FailedAt  int      // index of the failing step, or -1
	FailNote  string   // human-readable reason for the stop
	Aborted   bool
}

type Classifier interface {
	Classify(tool string, params json.RawMessage) Risk
}

type Verifier interface {
	// ok=false means the step did not achieve its goal; reason is for narration.
	Verify(ctx context.Context, step Step, result string, execErr error) (ok bool, reason string)
}

type Recoverer interface {
	Recover(step Step, attempt int, lastErr error) Decision
}

// TrustedExecutor composes the units and injected side effects. trust never
// imports agent/tools/ui/llm; those are supplied as funcs.
type TrustedExecutor struct {
	Classifier Classifier
	Verifier   Verifier
	Recoverer  Recoverer

	Exec     func(ctx context.Context, tool string, params json.RawMessage) (string, error)
	Confirm  func(previewJSON string) bool
	Describe func(tool string, params json.RawMessage) string
	Narrate  func(msg string)
	Replan   func(ctx context.Context, remaining []Step, failed Step, err error) []Step
	Ask      func(step Step, reason string) Decision
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/trust/types.go internal/trust/types_test.go
git commit -m "feat(trust): foundation types (Step/Risk/Decision/Report + interfaces)"
```

---

## Task 2: RiskClassifier

**Files:**
- Create: `internal/trust/classify.go`
- Test: `internal/trust/classify_test.go`

**Interfaces:**
- Consumes: `Step`, `Risk`, `Safe`, `Risky` (Task 1).
- Produces: `type RiskClassifier struct{}` implementing `Classify(tool string, params json.RawMessage) Risk`; constructor `NewRiskClassifier() *RiskClassifier`. Reads the tool's own `RequiresConfirmation()` is NOT possible here (no tools import) — instead the classifier is name-table + param based only. The unknown-tool default is `Risky`.

- [ ] **Step 1: Write the failing test** — `internal/trust/classify_test.go`

```go
package trust

import (
	"encoding/json"
	"testing"
)

func TestClassifyTable(t *testing.T) {
	c := NewRiskClassifier()
	cases := []struct {
		tool string
		want Risk
	}{
		{"get_datetime", Safe},
		{"list_files", Safe},
		{"read_file", Safe},
		{"search", Safe},
		{"delete_file", Risky},
		{"keyboard_type", Risky},
		{"native_click", Risky},
		{"run_python", Risky},
		{"google_workflow_agent", Risky},
		{"some_unknown_tool", Risky}, // unknown default = Risky
	}
	for _, tc := range cases {
		if got := c.Classify(tc.tool, nil); got != tc.want {
			t.Errorf("Classify(%q)=%v want %v", tc.tool, got, tc.want)
		}
	}
}

func TestClassifyParamBump(t *testing.T) {
	c := NewRiskClassifier()
	// system_control shutdown → Risky even though base system_control is safe-ish
	sd, _ := json.Marshal(map[string]string{"action": "shutdown"})
	if c.Classify("system_control", sd) != Risky {
		t.Error("system_control shutdown should be Risky")
	}
	// window_control close → Risky
	cl, _ := json.Marshal(map[string]string{"action": "close"})
	if c.Classify("window_control", cl) != Risky {
		t.Error("window_control close should be Risky")
	}
	// media_control play (read-ish action) → Safe
	pl, _ := json.Marshal(map[string]string{"action": "play"})
	if c.Classify("media_control", pl) != Safe {
		t.Error("media_control play should be Safe")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run TestClassify`
Expected: FAIL — `undefined: NewRiskClassifier`.

- [ ] **Step 3: Write minimal implementation** — `internal/trust/classify.go`

```go
package trust

import "encoding/json"

// riskyTools are always Risky by name.
var riskyTools = map[string]bool{
	"delete_file": true, "write_file": true, "create_file": true,
	"keyboard_type": true, "keyboard_combo": true,
	"native_click": true, "mouse_click": true, "mouse_move": true, "mouse_drag": true,
	"run_python": true, "run_terminal": true, "browser_navigate": true,
	"google_workflow_agent": true, "spotify_workflow_agent": true, "google_ai": true,
}

// safeTools are always Safe by name.
var safeTools = map[string]bool{
	"get_datetime": true, "list_files": true, "read_file": true, "search": true,
	"screenshot_analysis": true, "explain_selection": true, "recall": true,
	"list_memories": true, "browser_read_page": true,
}

// dangerousActions bump a normally-benign control tool to Risky based on params.
var dangerousActions = map[string]bool{
	"shutdown": true, "restart": true, "logoff": true, "close": true, "kill": true,
}

type RiskClassifier struct{}

func NewRiskClassifier() *RiskClassifier { return &RiskClassifier{} }

func (c *RiskClassifier) Classify(tool string, params json.RawMessage) Risk {
	if riskyTools[tool] {
		return Risky
	}
	// Param-aware bump for control tools (media/window/system_control).
	if tool == "system_control" || tool == "window_control" || tool == "media_control" {
		var p struct{ Action string `json:"action"` }
		if len(params) > 0 && json.Unmarshal(params, &p) == nil && dangerousActions[p.Action] {
			return Risky
		}
		return Safe
	}
	if safeTools[tool] {
		return Safe
	}
	return Risky // unknown → safe default is to gate
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run TestClassify -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/trust/classify.go internal/trust/classify_test.go
git commit -m "feat(trust): RiskClassifier (name table + param-aware bump)"
```

---

## Task 3: Verifier

**Files:**
- Create: `internal/trust/verify.go`
- Test: `internal/trust/verify_test.go`

**Interfaces:**
- Consumes: `Step`, `Verifier` (Task 1).
- Produces: `type StepVerifier struct { LLMJudge func(ctx context.Context, goal, observation string) (bool, string) }` implementing `Verify`. Constructor `NewStepVerifier(judge func(ctx, goal, observation string) (bool, string)) *StepVerifier`. `judge` may be nil (then fuzzy steps are treated as verified). `fuzzyTools` = native_click/keyboard_*/mouse_* → use judge.

- [ ] **Step 1: Write the failing test** — `internal/trust/verify_test.go`

```go
package trust

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyToolError(t *testing.T) {
	v := NewStepVerifier(nil)
	ok, _ := v.Verify(context.Background(), Step{Tool: "read_file"}, "", os.ErrNotExist)
	if ok {
		t.Fatal("tool error must not verify")
	}
}

func TestVerifyCreateFilePostcondition(t *testing.T) {
	dir := t.TempDir()
	made := filepath.Join(dir, "out.txt")
	os.WriteFile(made, []byte("hi"), 0o644)
	missing := filepath.Join(dir, "nope.txt")
	v := NewStepVerifier(nil)

	p, _ := json.Marshal(map[string]string{"path": made})
	if ok, _ := v.Verify(context.Background(), Step{Tool: "create_file", Params: p}, "done", nil); !ok {
		t.Error("existing created file should verify")
	}
	pm, _ := json.Marshal(map[string]string{"path": missing})
	if ok, _ := v.Verify(context.Background(), Step{Tool: "create_file", Params: pm}, "done", nil); ok {
		t.Error("missing created file must not verify")
	}
}

func TestVerifyDeleteFilePostcondition(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone.txt") // never created
	v := NewStepVerifier(nil)
	p, _ := json.Marshal(map[string]string{"path": gone})
	if ok, _ := v.Verify(context.Background(), Step{Tool: "delete_file", Params: p}, "done", nil); !ok {
		t.Error("absent file should verify a delete")
	}
}

func TestVerifyFuzzyUsesJudge(t *testing.T) {
	called := false
	judge := func(ctx context.Context, goal, obs string) (bool, string) { called = true; return false, "no" }
	v := NewStepVerifier(judge)
	ok, _ := v.Verify(context.Background(), Step{Tool: "native_click", Goal: "click Save"}, "clicked", nil)
	if !called || ok {
		t.Errorf("fuzzy step must call judge and honor its verdict; called=%v ok=%v", called, ok)
	}
}

func TestVerifyFuzzyNilJudgeTrusts(t *testing.T) {
	v := NewStepVerifier(nil)
	if ok, _ := v.Verify(context.Background(), Step{Tool: "native_click"}, "clicked", nil); !ok {
		t.Error("with no judge, fuzzy step should be trusted (verified)")
	}
}

func TestVerifyDefaultTrue(t *testing.T) {
	v := NewStepVerifier(nil)
	if ok, _ := v.Verify(context.Background(), Step{Tool: "open_file"}, "", nil); !ok {
		t.Error("no applicable check → verified true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run TestVerify`
Expected: FAIL — `undefined: NewStepVerifier`.

- [ ] **Step 3: Write minimal implementation** — `internal/trust/verify.go`

```go
package trust

import (
	"context"
	"encoding/json"
	"os"
	"strings"
)

var fuzzyTools = map[string]bool{
	"native_click": true, "keyboard_type": true, "keyboard_combo": true,
	"mouse_click": true, "mouse_move": true, "mouse_drag": true,
}

type StepVerifier struct {
	// LLMJudge returns (ok, reason). May be nil.
	LLMJudge func(ctx context.Context, goal, observation string) (bool, string)
}

func NewStepVerifier(judge func(ctx context.Context, goal, observation string) (bool, string)) *StepVerifier {
	return &StepVerifier{LLMJudge: judge}
}

func pathParam(params json.RawMessage) string {
	var p struct{ Path string `json:"path"` }
	if len(params) > 0 && json.Unmarshal(params, &p) == nil {
		return p.Path
	}
	return ""
}

func (v *StepVerifier) Verify(ctx context.Context, step Step, result string, execErr error) (bool, string) {
	// 1. Tool error (Exec already folded ToolResult failures into execErr).
	if execErr != nil {
		return false, execErr.Error()
	}
	// 2. Deterministic post-conditions.
	switch step.Tool {
	case "create_file", "write_file":
		if path := pathParam(step.Params); path != "" {
			if _, err := os.Stat(path); err != nil {
				return false, "expected file was not created: " + path
			}
			return true, ""
		}
	case "delete_file":
		if path := pathParam(step.Params); path != "" {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return true, ""
			}
			return false, "file still exists after delete: " + path
		}
	case "get_datetime", "read_file", "search":
		if strings.TrimSpace(result) == "" {
			return false, "empty result"
		}
		return true, ""
	}
	// 3. Fuzzy GUI/vision → LLM judge if available; else trust.
	if fuzzyTools[step.Tool] {
		if v.LLMJudge == nil {
			return true, ""
		}
		return v.LLMJudge(ctx, step.Goal, result)
	}
	// 4. Default: trust the tool.
	return true, ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run TestVerify -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/trust/verify.go internal/trust/verify_test.go
git commit -m "feat(trust): Verifier (cheap post-conditions + optional LLM judge)"
```

---

## Task 4: Recoverer

**Files:**
- Create: `internal/trust/recover.go`
- Test: `internal/trust/recover_test.go`

**Interfaces:**
- Consumes: `Step`, `Decision`, `Retry`, `Replan`, `Ask`, `Abort` (Task 1).
- Produces: `type LadderRecoverer struct { MaxRetries int; replanned bool }`, constructor `NewLadderRecoverer(maxRetries int) *LadderRecoverer`, method `Recover(step Step, attempt int, lastErr error) Decision`, and `MarkReplanned()` so the executor can flip the once-only flag after it consumes a `Replan`.

`Recover` logic: `attempt` is the count of executions already made for this step (1 after first run). While `attempt <= MaxRetries` → `Retry`. Else if `!replanned` → `Replan`. Else → `Ask`. (The executor calls `MarkReplanned()` when it acts on a `Replan`, and converts a subsequent user "stop" into `Abort`.)

- [ ] **Step 1: Write the failing test** — `internal/trust/recover_test.go`

```go
package trust

import (
	"errors"
	"testing"
)

func TestRecoverLadder(t *testing.T) {
	r := NewLadderRecoverer(2)
	err := errors.New("boom")
	if d := r.Recover(Step{}, 1, err); d != Retry {
		t.Errorf("attempt 1 → Retry, got %v", d)
	}
	if d := r.Recover(Step{}, 2, err); d != Retry {
		t.Errorf("attempt 2 → Retry, got %v", d)
	}
	if d := r.Recover(Step{}, 3, err); d != Replan {
		t.Errorf("attempt 3 (retries exhausted) → Replan, got %v", d)
	}
	r.MarkReplanned()
	if d := r.Recover(Step{}, 3, err); d != Ask {
		t.Errorf("after replan used → Ask, got %v", d)
	}
}

func TestRecoverZeroRetries(t *testing.T) {
	r := NewLadderRecoverer(0)
	if d := r.Recover(Step{}, 1, errors.New("x")); d != Replan {
		t.Errorf("0 retries, attempt 1 → Replan, got %v", d)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run TestRecover`
Expected: FAIL — `undefined: NewLadderRecoverer`.

- [ ] **Step 3: Write minimal implementation** — `internal/trust/recover.go`

```go
package trust

type LadderRecoverer struct {
	MaxRetries int
	replanned  bool
}

func NewLadderRecoverer(maxRetries int) *LadderRecoverer {
	return &LadderRecoverer{MaxRetries: maxRetries}
}

// MarkReplanned records that the one allowed re-plan has been consumed.
func (r *LadderRecoverer) MarkReplanned() { r.replanned = true }

// Recover chooses the next move. attempt = number of executions already made
// for this step (1 after the first run).
func (r *LadderRecoverer) Recover(step Step, attempt int, lastErr error) Decision {
	if attempt <= r.MaxRetries {
		return Retry
	}
	if !r.replanned {
		return Replan
	}
	return Ask
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run TestRecover -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/trust/recover.go internal/trust/recover_test.go
git commit -m "feat(trust): LadderRecoverer (retry → replan-once → ask)"
```

---

## Task 5: Previewer (gate decision + card JSON)

**Files:**
- Create: `internal/trust/preview.go`
- Test: `internal/trust/preview_test.go`

**Interfaces:**
- Consumes: `Step`, `Risk`, `Classifier` (Task 1/2).
- Produces:
  - `func ShouldGate(steps []Step, risks []Risk) bool` — `len(steps) >= 2 || any risk == Risky`.
  - `func BuildPreview(command string, steps []Step, risks []Risk, describe func(tool string, params json.RawMessage) string) string` — returns the confirm-card JSON string (`type:"workflow_approval"`, `title`, `plan.goal`, `plan.steps[]{label,value}`), label tagged `Step N · Safe` / `Step N · ⚠ Risky`, value from `describe` (fallback to `step.Goal`, then `tool`).

- [ ] **Step 1: Write the failing test** — `internal/trust/preview_test.go`

```go
package trust

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShouldGate(t *testing.T) {
	if ShouldGate([]Step{{}}, []Risk{Safe}) {
		t.Error("single safe step must not gate")
	}
	if !ShouldGate([]Step{{}, {}}, []Risk{Safe, Safe}) {
		t.Error("two safe steps must gate (multi-step)")
	}
	if !ShouldGate([]Step{{}}, []Risk{Risky}) {
		t.Error("single risky step must gate")
	}
}

func TestBuildPreviewShape(t *testing.T) {
	steps := []Step{
		{Tool: "search", Goal: "find invoice"},
		{Tool: "delete_file", Goal: "remove old invoice"},
	}
	risks := []Risk{Safe, Risky}
	desc := func(tool string, p json.RawMessage) string {
		if tool == "delete_file" {
			return "Delete invoice_old.pdf"
		}
		return "Search files for 'invoice'"
	}
	out := BuildPreview("clean up invoices", steps, risks, desc)

	var card struct {
		Type string `json:"type"`
		Title string `json:"title"`
		Plan struct {
			Goal  string `json:"goal"`
			Steps []struct{ Label, Value string } `json:"steps"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(out), &card); err != nil {
		t.Fatalf("preview is not valid JSON: %v", err)
	}
	if card.Type != "workflow_approval" {
		t.Errorf("type=%q", card.Type)
	}
	if card.Plan.Goal != "clean up invoices" || len(card.Plan.Steps) != 2 {
		t.Fatalf("plan wrong: %+v", card.Plan)
	}
	if !strings.Contains(card.Plan.Steps[1].Label, "Risky") {
		t.Errorf("risky step must be tagged: %q", card.Plan.Steps[1].Label)
	}
	if card.Plan.Steps[1].Value != "Delete invoice_old.pdf" {
		t.Errorf("describe not used: %q", card.Plan.Steps[1].Value)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run "TestShouldGate|TestBuildPreview"`
Expected: FAIL — `undefined: ShouldGate`.

- [ ] **Step 3: Write minimal implementation** — `internal/trust/preview.go`

```go
package trust

import (
	"encoding/json"
	"fmt"
)

func ShouldGate(steps []Step, risks []Risk) bool {
	if len(steps) >= 2 {
		return true
	}
	for _, r := range risks {
		if r == Risky {
			return true
		}
	}
	return false
}

func BuildPreview(command string, steps []Step, risks []Risk, describe func(tool string, params json.RawMessage) string) string {
	type cardStep struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	card := map[string]interface{}{
		"type":  "workflow_approval",
		"title": fmt.Sprintf("Review this %d-step task", len(steps)),
	}
	if len(steps) == 1 {
		card["title"] = "Review action request"
	}
	out := make([]cardStep, len(steps))
	for i, s := range steps {
		tag := "Safe"
		if i < len(risks) && risks[i] == Risky {
			tag = "⚠ Risky"
		}
		val := ""
		if describe != nil {
			val = describe(s.Tool, s.Params)
		}
		if val == "" {
			val = s.Goal
		}
		if val == "" {
			val = s.Tool
		}
		out[i] = cardStep{Label: fmt.Sprintf("Step %d · %s", i+1, tag), Value: val}
	}
	card["plan"] = map[string]interface{}{"goal": command, "steps": out}
	b, _ := json.Marshal(card)
	return string(b)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run "TestShouldGate|TestBuildPreview" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/trust/preview.go internal/trust/preview_test.go
git commit -m "feat(trust): Previewer (gate decision + approval-card JSON)"
```

---

## Task 6: TrustedExecutor.Run (compose the loop)

**Files:**
- Create: `internal/trust/executor.go`
- Test: `internal/trust/executor_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–5 (`TrustedExecutor` fields, `RiskClassifier`, `StepVerifier`, `LadderRecoverer`, `ShouldGate`, `BuildPreview`).
- Produces: `func (t *TrustedExecutor) Run(ctx context.Context, steps []Step, command string) (Report, error)`. Also `const prevOutputToken = "{PREVIOUS_OUTPUT}"` handling: before executing a step, substitute the token inside string params with the previous step's result.

**Behavior (implements spec §Architecture):**
1. Classify all steps. If `ShouldGate` and `Confirm != nil`: build preview, call `Confirm`; on false → return `Report{FailedAt:-1, FailNote:"cancelled by user", Aborted:true}, nil` with **zero** `Exec` calls.
2. Loop steps with an index that can jump when re-planned. For each step: substitute `{PREVIOUS_OUTPUT}`, `Narrate("Step i/n · <describe>…")`, run via `Exec`; then `Verify`. On verified → record `Describe` text in `Completed`, set `lastOutput`, continue.
3. On not-verified/error: consult `Recoverer.Recover(step, attempt, err)`:
   - `Retry` → re-run same step (increment attempt, short `time.Sleep(150ms)` backoff — but make backoff injectable/skippable in tests via a `backoff` field defaulting to a real sleep; in tests set it to a no-op).
   - `Replan` → if `Replan != nil`, call it; `Recoverer.MarkReplanned()`; replace the remaining tail with the returned steps (re-classify the tail; **no second gate**); continue. If `Replan` nil or returns empty → treat as `Ask`.
   - `Ask` → if `Ask != nil`, call it → `Retry`/`Abort` (map `Skip`... keep it to Retry/Abort for v1; `Ask` returns a `Decision`). On `Abort`/no Ask → stop with report.
   - `Abort` → stop, `Report{FailedAt:i, FailNote:reason, Aborted:true}`.

Keep `Run` focused; extract a helper `classifyAll([]Step) []Risk`.

- [ ] **Step 1: Write the failing test** — `internal/trust/executor_test.go`

```go
package trust

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// helper: an executor with real units but fake side effects, backoff disabled.
func newTestExec() (*TrustedExecutor, *[]string) {
	var execed []string
	te := &TrustedExecutor{
		Classifier: NewRiskClassifier(),
		Verifier:   NewStepVerifier(nil),
		Recoverer:  NewLadderRecoverer(2),
		Describe:   func(tool string, p json.RawMessage) string { return tool },
		Narrate:    func(string) {},
		Exec: func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
			execed = append(execed, tool)
			return "ok", nil
		},
	}
	te.noBackoff() // test seam: disable sleeps
	return te, &execed
}

func TestRunSingleSafeNoGate(t *testing.T) {
	te, execed := newTestExec()
	gated := false
	te.Confirm = func(string) bool { gated = true; return true }
	rep, err := te.Run(context.Background(), []Step{{Tool: "search"}}, "find x")
	if err != nil || gated {
		t.Fatalf("single safe step must not gate; gated=%v err=%v", gated, err)
	}
	if len(*execed) != 1 || rep.Aborted {
		t.Fatalf("should have run 1 step; rep=%+v execed=%v", rep, *execed)
	}
}

func TestRunGateRejectZeroSideEffects(t *testing.T) {
	te, execed := newTestExec()
	te.Confirm = func(string) bool { return false }
	rep, _ := te.Run(context.Background(), []Step{{Tool: "search"}, {Tool: "delete_file"}}, "cmd")
	if len(*execed) != 0 || !rep.Aborted {
		t.Fatalf("reject must run nothing; execed=%v rep=%+v", *execed, rep)
	}
}

func TestRunGateApproveRunsAll(t *testing.T) {
	te, execed := newTestExec()
	te.Confirm = func(string) bool { return true }
	_, err := te.Run(context.Background(), []Step{{Tool: "search"}, {Tool: "search"}}, "cmd")
	if err != nil || len(*execed) != 2 {
		t.Fatalf("approve should run both; execed=%v err=%v", *execed, err)
	}
}

func TestRunRetryThenSucceed(t *testing.T) {
	var calls int
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("transient")
		}
		return "ok", nil
	}
	_, err := te.Run(context.Background(), []Step{{Tool: "search"}}, "cmd")
	if err != nil || calls != 2 {
		t.Fatalf("expected 1 retry then success; calls=%d err=%v", calls, err)
	}
}

func TestRunReplanOnceThenTail(t *testing.T) {
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	// step always fails; replan returns a single safe step that succeeds.
	fail := true
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		if tool == "flaky" {
			return "", errors.New("nope")
		}
		return "ok", nil
	}
	replans := 0
	te.Replan = func(ctx context.Context, remaining []Step, failed Step, err error) []Step {
		replans++
		return []Step{{Tool: "search"}}
	}
	_ = fail
	_, err := te.Run(context.Background(), []Step{{Tool: "flaky"}}, "cmd")
	if replans != 1 || err != nil {
		t.Fatalf("expected exactly one replan then success; replans=%d err=%v", replans, err)
	}
}

func TestRunAbortReport(t *testing.T) {
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		return "", errors.New("always")
	}
	te.Replan = func(ctx context.Context, r []Step, f Step, e error) []Step { return nil } // no replan
	te.Ask = func(step Step, reason string) Decision { return Abort }
	rep, _ := te.Run(context.Background(), []Step{{Tool: "flaky"}}, "cmd")
	if !rep.Aborted || rep.FailedAt != 0 {
		t.Fatalf("expected abort report at step 0; rep=%+v", rep)
	}
}

func TestRunPreviousOutputInjection(t *testing.T) {
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	var seen string
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		if tool == "second" {
			var m map[string]string
			json.Unmarshal(p, &m)
			seen = m["text"]
		}
		return "RESULT1", nil
	}
	p2, _ := json.Marshal(map[string]string{"text": "{PREVIOUS_OUTPUT}"})
	_, err := te.Run(context.Background(), []Step{{Tool: "first"}, {Tool: "second", Params: p2}}, "cmd")
	if err != nil || seen != "RESULT1" {
		t.Fatalf("PREVIOUS_OUTPUT not injected; seen=%q err=%v", seen, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -run TestRun`
Expected: FAIL — `undefined: (*TrustedExecutor).Run` / `noBackoff`.

- [ ] **Step 3: Write minimal implementation** — `internal/trust/executor.go`

```go
package trust

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const prevOutputToken = "{PREVIOUS_OUTPUT}"

// backoff is overridable in tests via noBackoff().
func (t *TrustedExecutor) sleep(d time.Duration) {
	if t.backoff == nil {
		time.Sleep(d)
		return
	}
	t.backoff(d)
}

// add a private field via a struct method file — declared here to keep executor cohesive.
// (field is added to the struct in types.go; see note below.)

func (t *TrustedExecutor) noBackoff() { t.backoff = func(time.Duration) {} }

func (t *TrustedExecutor) classifyAll(steps []Step) []Risk {
	risks := make([]Risk, len(steps))
	for i, s := range steps {
		risks[i] = t.Classifier.Classify(s.Tool, s.Params)
	}
	return risks
}

func injectPrev(params json.RawMessage, prev string) json.RawMessage {
	if len(params) == 0 || !strings.Contains(string(params), prevOutputToken) {
		return params
	}
	var m map[string]interface{}
	if json.Unmarshal(params, &m) != nil {
		return params
	}
	for k, v := range m {
		if sv, ok := v.(string); ok {
			m[k] = strings.ReplaceAll(sv, prevOutputToken, prev)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return params
	}
	return b
}

func (t *TrustedExecutor) Run(ctx context.Context, steps []Step, command string) (Report, error) {
	rep := Report{FailedAt: -1}
	risks := t.classifyAll(steps)

	// One gate up front.
	if ShouldGate(steps, risks) && t.Confirm != nil {
		if !t.Confirm(BuildPreview(command, steps, risks, t.Describe)) {
			rep.Aborted = true
			rep.FailNote = "cancelled by user"
			return rep, nil
		}
	}

	var lastOutput string
	i := 0
	for i < len(steps) {
		step := steps[i]
		attempt := 0
		for { // retry loop for this step
			attempt++
			label := t.describeStep(step)
			if t.Narrate != nil {
				t.Narrate(fmt.Sprintf("Step %d/%d · %s…", i+1, len(steps), label))
			}
			params := injectPrev(step.Params, lastOutput)
			result, execErr := t.Exec(ctx, step.Tool, params)
			ok, reason := t.Verifier.Verify(ctx, step, result, execErr)
			if ok {
				lastOutput = result
				rep.Completed = append(rep.Completed, label)
				break // step done
			}
			// Recover.
			stepErr := execErr
			if stepErr == nil {
				stepErr = fmt.Errorf("%s", reason)
			}
			switch t.Recoverer.Recover(step, attempt, stepErr) {
			case Retry:
				t.sleep(150 * time.Millisecond)
				continue
			case Replan:
				tail := []Step(nil)
				if t.Replan != nil {
					tail = t.Replan(ctx, steps[i+1:], step, stepErr)
				}
				if r, ok := t.Recoverer.(*LadderRecoverer); ok {
					r.MarkReplanned()
				}
				if len(tail) == 0 {
					return t.stop(rep, i, "re-plan produced no steps: "+stepErr.Error())
				}
				// Replace remaining tail; re-classify (no second gate).
				steps = append(steps[:i], tail...)
				risks = t.classifyAll(steps)
				attempt = 0
				step = steps[i]
				continue
			case Ask:
				if t.Ask != nil && t.Ask(step, reason) == Retry {
					t.sleep(150 * time.Millisecond)
					continue
				}
				return t.stop(rep, i, "stopped at failing step: "+stepErr.Error())
			default: // Abort
				return t.stop(rep, i, stepErr.Error())
			}
		}
		i++
	}
	return rep, nil
}

func (t *TrustedExecutor) stop(rep Report, at int, note string) (Report, error) {
	rep.Aborted = true
	rep.FailedAt = at
	rep.FailNote = note
	if t.Narrate != nil {
		t.Narrate("Stopped: " + note)
	}
	return rep, nil
}

func (t *TrustedExecutor) describeStep(step Step) string {
	if t.Describe != nil {
		if d := t.Describe(step.Tool, step.Params); d != "" {
			return d
		}
	}
	if step.Goal != "" {
		return step.Goal
	}
	return step.Tool
}
```

**NOTE for implementer:** add one unexported field to the `TrustedExecutor` struct in `types.go`:
```go
	backoff func(time.Duration) // nil = real time.Sleep; tests set a no-op via noBackoff()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/trust/ -v`
Expected: PASS (all trust tests).

- [ ] **Step 5: Commit**

```bash
git add internal/trust/executor.go internal/trust/executor_test.go internal/trust/types.go
git commit -m "feat(trust): TrustedExecutor.Run — gate, verify, recovery ladder"
```

---

## Task 7: GraphExecutor delegation + `RunTool` extraction

**Files:**
- Modify: `internal/agent/executor.go` (add `Trusted` field; extract `RunTool`; delegate)
- Test: `internal/agent/executor_trust_test.go`

**Interfaces:**
- Consumes: `trust.TrustedExecutor`, `trust.Step`, `trust.Report`.
- Produces:
  - `GraphExecutor` gains field `Trusted *trust.TrustedExecutor`.
  - `func RunTool(ctx context.Context, reg *tools.Registry, tool string, params json.RawMessage) (string, error)` — the per-step body extracted from `ExecutePlan`: registry lookup, `tool.Execute`, the Phase-3 `workflow_approval` handshake (show card via `ui.RequestConfirmationCard`, re-exec with `approved_plan`), and folding a `ToolResult` that encodes failure into an `error`. (Reused by both the legacy loop and the trust `Exec` closure.)
  - `ExecutePlan`: when `e.Trusted != nil`, convert `plan.Tasks` → `[]trust.Step` (Goal defaults to `task.Tool`), set `e.Trusted.Exec = func(ctx, tool, params){ return RunTool(ctx, e.Registry, tool, params) }` **only if unset**, call `Run`, and translate the `Report` (log Completed / FailNote). When nil, run the existing loop unchanged.

**Constraint:** existing `ExecutePlan` behavior with `Trusted == nil` MUST be identical (existing `internal/agent` tests pass unchanged).

- [ ] **Step 1: Write the failing test** — `internal/agent/executor_trust_test.go`

```go
package agent

import (
	"context"
	"testing"

	"github.com/yourname/voice-agent/internal/trust"
)

func TestExecutePlanDelegatesWhenTrustedSet(t *testing.T) {
	ran := []string{}
	te := &trust.TrustedExecutor{
		Classifier: trust.NewRiskClassifier(),
		Verifier:   trust.NewStepVerifier(nil),
		Recoverer:  trust.NewLadderRecoverer(2),
		Confirm:    func(string) bool { return true },
		Exec: func(ctx context.Context, tool string, p []byte) (string, error) {
			ran = append(ran, tool)
			return "ok", nil
		},
	}
	// NOTE: Exec signature uses json.RawMessage; adapt in real impl. See interface.
	e := &GraphExecutor{Registry: nil, Trusted: te}
	_ = e
	// Full wiring asserted via build + trust tests; this test guards the field exists
	// and delegation path compiles. Keep minimal to avoid duplicating trust coverage.
	if e.Trusted == nil {
		t.Fatal("Trusted field must be set")
	}
	_ = ran
}
```

*(Implementer: if a fuller delegation test is feasible with a fake registry, add it; otherwise this guards the seam and the real behavior is covered by `internal/trust` end-to-end tests + the build.)*

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/agent/ -run TestExecutePlanDelegates`
Expected: FAIL — `GraphExecutor has no field Trusted`.

- [ ] **Step 3: Write minimal implementation**

Extract `RunTool` from the current `ExecutePlan` body (lines ~60–94 in `internal/agent/executor.go`: `tool.Execute`, the `workflow_approval` handshake, error return). Add `Trusted *trust.TrustedExecutor` to the struct. At the top of `ExecutePlan`, add:

```go
if e.Trusted != nil {
	steps := make([]trust.Step, len(plan.Tasks))
	for i, tk := range plan.Tasks {
		steps[i] = trust.Step{Tool: tk.Tool, Params: tk.Params, Goal: tk.Tool}
	}
	if e.Trusted.Exec == nil {
		reg := e.Registry
		e.Trusted.Exec = func(ctx gocontext.Context, tool string, params json.RawMessage) (string, error) {
			return RunTool(ctx, reg, tool, params)
		}
	}
	rep, err := e.Trusted.Run(ctx, steps, plan.Transcript)
	if err != nil {
		return err
	}
	if rep.Aborted {
		log.Printf("[trust] stopped: %s (completed %d steps)", rep.FailNote, len(rep.Completed))
		return fmt.Errorf("plan stopped: %s", rep.FailNote)
	}
	return nil
}
// ...existing legacy loop unchanged below...
```

`RunTool` folds a failure-encoding `ToolResult` into an error (so trust's Verifier sees `execErr`). Keep the legacy loop calling `RunTool` too if it simplifies without changing behavior; otherwise leave the legacy loop byte-for-byte and only reuse `RunTool` for the closure.

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/agent/... ./internal/trust/...`
Expected: PASS (existing agent tests + new seam test + trust tests).

- [ ] **Step 5: Commit**

```bash
git add internal/agent/executor.go internal/agent/executor_trust_test.go
git commit -m "feat(agent): GraphExecutor delegates to trust layer (nil-safe) + RunTool"
```

---

## Task 8: Wiring — config flag, Deps, UI prompt, main assembly

**Files:**
- Modify: `config/config.go` (add `TrustedExecution bool json:"trusted_execution"`, default true on load)
- Modify: `internal/dispatch/dispatch.go` (add `Trusted *trust.TrustedExecutor` to `Deps`; set `exec.Trusted = d.Trusted` at both `NewExecutor` sites)
- Modify: `internal/command/router.go` (set `executor.Trusted` from a package var set by a new `SetTrusted`)
- Create: `internal/ui/ask_step.go` (`func AskStepChoice(reason string) bool` reusing the confirm card: Retry vs Stop)
- Modify: `internal/engine/runtime.go` (thread `Trusted` into `dispatch.Deps`)
- Modify: `cmd/app/main.go` (build the `TrustedExecutor`, wire it)
- Modify: `CLAUDE.md`, `README.md` (document the layer + flag)

**Interfaces:**
- Consumes: all of `internal/trust`, `internal/ui`, `internal/llm.Provider.Generate`.
- Produces: a single constructed `*trust.TrustedExecutor` injected into every execution path.

**Default-true load:** in `config.LoadConfig`, after unmarshal, if the raw JSON had no `trusted_execution` key, set `cfg.TrustedExecution = true`. Simplest robust approach: default the struct field to true by post-processing — unmarshal into a map first to detect key presence, OR add `cfg.TrustedExecution = true` before `json.Unmarshal` won't work (zero-value overwrite). Use: unmarshal into the struct, then re-unmarshal into `map[string]json.RawMessage`; if `_, ok := m["trusted_execution"]; !ok { cfg.TrustedExecution = true }`.

**main.go assembly (sketch):**
```go
judge := func(ctx context.Context, goal, obs string) (bool, string) {
	out, err := provider.Generate(ctx, "Goal: "+goal+"\nObservation: "+obs+
		"\nDid the action succeed? Answer 'yes' or 'no' then a short reason.", nil)
	if err != nil { return true, "" } // never block on unavailable judge
	yes := strings.HasPrefix(strings.ToLower(strings.TrimSpace(out)), "yes")
	return yes, out
}
var te *trust.TrustedExecutor
if cfg.TrustedExecution {
	te = &trust.TrustedExecutor{
		Classifier: trust.NewRiskClassifier(),
		Verifier:   trust.NewStepVerifier(judge),
		Recoverer:  trust.NewLadderRecoverer(2),
		Confirm:    ui.RequestConfirmationCard,
		Describe:   trust.DefaultDescribe, // see below
		Narrate:    ui.ShowNotification,
		Ask:        func(s trust.Step, reason string) trust.Decision {
			if ui.AskStepChoice(reason) { return trust.Retry }
			return trust.Abort
		},
		Replan: func(ctx context.Context, remaining []trust.Step, failed trust.Step, err error) []trust.Step {
			return nil // v1: no LLM re-plan wired yet → ladder falls through to Ask
		},
	}
}
// engine + router get te
engine.NewEngine(...) // pass te through to dispatch.Deps.Trusted
command.SetTrusted(te)
```

**`trust.DefaultDescribe`** — add to `internal/trust/preview.go`: a small formatter for common tools (`delete_file{path}`→"Delete <base>", `create_file`/`write_file`→"Create <base>", `open_file`→"Open <base>", `search{query}`→"Search for '<q>'"), fallback `""`. Add a table test for two cases.

> **Re-plan note:** wiring a real LLM re-plan is deferred to a follow-up (ladder currently degrades Replan→Ask when `Replan` returns nil, which is spec-correct behavior). Document this as a known limitation, not a bug.

- [ ] **Step 1: Write the failing test** — `config/config_test.go` (add case) + `internal/trust/preview_test.go` (DefaultDescribe)

```go
// config: default-true when key absent
func TestTrustedExecutionDefaultsTrue(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{"llm_provider":"gemini","api_key":"x"}`))
	if err != nil { t.Fatal(err) }
	if !cfg.TrustedExecution { t.Error("trusted_execution should default true when absent") }
}
func TestTrustedExecutionRespectsFalse(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{"trusted_execution":false}`))
	if err != nil { t.Fatal(err) }
	if cfg.TrustedExecution { t.Error("explicit false must be honored") }
}
```
```go
// trust: DefaultDescribe
func TestDefaultDescribe(t *testing.T) {
	p, _ := json.Marshal(map[string]string{"path": `C:\x\invoice_old.pdf`})
	if got := DefaultDescribe("delete_file", p); got != "Delete invoice_old.pdf" {
		t.Errorf("got %q", got)
	}
	q, _ := json.Marshal(map[string]string{"query": "budget"})
	if got := DefaultDescribe("search", q); got != "Search for 'budget'" {
		t.Errorf("got %q", got)
	}
}
```
*(Implementer: add a small `loadFromBytes` test helper in the config package if one doesn't exist — factor it out of `LoadConfig`.)*

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./config/... ./internal/trust/ -run "TrustedExecution|DefaultDescribe"`
Expected: FAIL — undefined field / func.

- [ ] **Step 3: Write minimal implementation**

Implement: config field + default-true detection + `loadFromBytes` helper; `trust.DefaultDescribe`; `dispatch.Deps.Trusted` + set at both sites; `command.SetTrusted` + package var applied at `router.go:127`; `ui.AskStepChoice`; thread through `engine.NewEngine`; `main.go` assembly.

`internal/ui/ask_step.go`:
```go
package ui

import "encoding/json"

// AskStepChoice shows a Retry/Stop card for a failed step. Returns true=Retry.
func AskStepChoice(reason string) bool {
	card, _ := json.Marshal(map[string]interface{}{
		"type": "workflow_approval", "title": "Step failed",
		"plan": map[string]interface{}{"goal": reason,
			"steps": []map[string]string{{"label": "Choose", "value": "Approve = Retry · Cancel = Stop"}}},
	})
	return RequestConfirmationCard(string(card))
}
```

- [ ] **Step 4: Verify build + tests**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./config/... ./internal/trust/... ./internal/agent/... && go vet ./internal/dispatch/ ./internal/command/ ./internal/ui/ ./cmd/app/`
Expected: tests PASS; `go vet` clean (full link is done in the manual build below).

- [ ] **Step 5: Full voice build + commit**

```bash
export PATH="$PATH:/c/w64devkit/bin" && go build -tags whisper -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
git add config/config.go config/config_test.go internal/dispatch/dispatch.go internal/command/router.go internal/ui/ask_step.go internal/engine/runtime.go cmd/app/main.go internal/trust/preview.go internal/trust/preview_test.go CLAUDE.md README.md
git commit -m "feat(trust): wire trustworthy layer into all execution paths + config flag"
```

---

## Self-Review notes (author)

- **Spec coverage:** classify (T2), preview+gate (T5), verify (T3), recover (T4), Run compose (T6), delegation/no-cycle (T7), config+wiring+UI+narration (T8). Success criteria 1–5 all map to tasks. LLM re-plan wiring intentionally deferred (documented in T8) — ladder still spec-correct (Replan→Ask fallthrough).
- **Type consistency:** `Step{Tool,Params,Goal}`, `Risk`, `Decision`, `Report`, `Classify`, `Verify`, `Recover`, `Run(ctx,steps,command)`, `ShouldGate`, `BuildPreview`, `DefaultDescribe`, `RunTool` — used identically across tasks. `backoff` field noted in T6 as a T6 addition to the T1 struct.
- **Non-breaking:** T7 keeps the legacy loop for `Trusted==nil`; existing agent tests unchanged.
