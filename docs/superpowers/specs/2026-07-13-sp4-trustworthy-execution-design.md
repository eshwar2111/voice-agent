# SP4 — Trustworthy One-Shot Execution Layer (Design Spec)

**Date:** 2026-07-13
**Status:** Approved (design), pending implementation plan
**Builds on:** SP1 (tiered resolver + `GraphExecutor`), SP2 (ambient context), SP3 (ambient triggers + overlay redesign).

---

## Program context

Fourth and final slice of the low-overhead tiered-intelligence assistant. SP1–SP3 gave the
assistant fast local dispatch, deep context, and proactive suggestions. Multi-step execution
already exists — `agent.Orchestrator` decomposes a request into sub-goals and
`agent.GraphExecutor.ExecutePlan` runs task graphs; the Google/Spotify workflow agents build
their own step plans with an approval card.

What is **missing is trust**: steps execute blind (no check they achieved their goal), the
executor **aborts on the first error** with no recovery, approval is a coarse whole-plan
yes/no, and the riskiest surface — desktop GUI automation (robotgo clicks, keyboard, vision) —
has the least safety. SP4 adds one **unified trustworthy execution layer** so that saying a
whole task out loud, or typing it, runs **reliably and safely**.

Compute strategy unchanged (cloud-LLM-only, BYOK, low idle overhead). The layer is
**deterministic-first**: it spends zero tokens on the common cases and reserves the LLM for the
two genuinely un-checkable moments (judging a fuzzy GUI step, re-planning after a failure).

---

## Goal

Make one-shot, multi-step automation **trustworthy** by wrapping every plan — regardless of
surface — in a single layer that: (1) previews the plan and gates on approval before any side
effect, (2) verifies each step actually worked, and (3) recovers from failures on a bounded
ladder, narrating throughout.

## Success criteria

1. **See it before it runs.** A plan that is multi-step **or** contains any risky step shows a
   single plain-English preview + approve/cancel card **before any side effect**; rejecting it
   leaves the system untouched (zero side effects).
2. **Runs safely by risk.** A safe single step (open app, search, read) runs immediately with no
   gate. Risky steps (delete, overwrite, GUI click/type, external send, destructive system
   control) are always inside a gated plan.
3. **Recovers when steps fail.** A failed or unverified step triggers a bounded ladder — retry
   (≤2, no LLM) → re-plan the remainder once → ask the user — instead of aborting. On an
   unrecoverable stop, the user gets a report of what completed and what failed.
4. **Cheap by default.** Deterministic steps verify for free; the LLM judge runs only for fuzzy
   GUI/vision steps with no deterministic check, and re-planning runs at most **once per plan**.
   No LLM call happens for a fully-deterministic successful plan.
5. **Non-breaking.** With the layer unwired (`Trusted == nil`) `ExecutePlan` behaves exactly as
   today; all existing tests pass unchanged.

---

## Architecture

New package **`internal/trust`**. A `TrustedExecutor` becomes the step-runner that
`GraphExecutor.ExecutePlan` delegates to, so both execution paths that exist today —
`dispatch.Handle` (Tier 0 resolver plans) and `Orchestrator.execSubGoal` (which wraps each
sub-goal into a one-task plan, `orchestrator.go:108`) — are covered at **one choke point**.

```
dispatch / orchestrator
        │  agent.Plan{Tasks}
        ▼
GraphExecutor.ExecutePlan ──► trust.TrustedExecutor.Run(ctx, steps, command)
                                   │
             ┌─────────────────────┼──────────────────────────────┐
             ▼                     ▼                                ▼
      1. classify all      2. GATE (once, up front)         3. per-step loop:
         steps → Risk         if len(Tasks)>=2 OR anyRisky:     execute → verify
                              Previewer builds plain-English      │ ok  → narrate → next
                              plan → Confirm(previewJSON)         │ fail → Recoverer ladder
                              reject → abort, 0 side effects            (retry/replan/ask/abort)
```

**One gate, up front** — not per-step nagging. The whole plan is classified once; if it is
multi-step or contains any risky step, a single preview+confirm card shows the full plan before
**any** side effect. On reject nothing ran. A safe single step skips the gate.

**Per-step verify + recover** happen during the loop. Deterministic steps verify for free; only
fuzzy GUI/vision steps may invoke the LLM judge. On a failed/unverified step the Recoverer
ladder runs. Re-planned tail steps re-enter classify/verify but **not** a second gate (they are
surfaced in the live narration instead), to avoid mid-run nagging.

Workflow agents (Google/Spotify) run their own internal steps and are **opaque single steps**
to this layer; their existing per-tool `RequiresConfirmation()` Phase-3 plan card stays as an
**inner** gate when they expand. The trust gate is the **outer**, whole-plan checkpoint.

### Dependency direction (no import cycle)

`internal/trust` **imports nothing from `internal/agent` or `internal/tools`** — only the
standard library. All coupling flows *inward* through local types and injected functions.
`agent.GraphExecutor` (which imports `trust` for its `Trusted` field) converts its `[]Task` into
`[]trust.Step` and provides the `Exec` closure. This keeps the dependency one-directional
(`agent → trust`, never back) and makes `TrustedExecutor` unit-testable with pure fakes.

### Interfaces (the seam)

```go
// Step is one unit of a plan, carrying the plain-English intent for previews and LLM judging.
// trust owns this type; agent converts its Task into it.
type Step struct {
    Tool   string
    Params json.RawMessage
    Goal   string // plain-English intent, e.g. "delete the old invoice"
}

type Risk int
const ( Safe Risk = iota; Risky )

type Decision int
const ( Retry Decision = iota; Replan; Ask; Abort )

// Report is returned by Run so the caller can render what completed vs. failed.
type Report struct {
    Completed []string // Describe() text of steps that ran and verified
    FailedAt  int      // index of the failing step, or -1
    FailNote  string   // human-readable reason for the stop
    Aborted   bool
}

type Classifier interface { Classify(tool string, params json.RawMessage) Risk }
type Verifier interface {
    // ok=false means the step did not achieve its goal; reason is for narration/report.
    Verify(ctx context.Context, step Step, result string, execErr error) (ok bool, reason string)
}
type Recoverer interface { Recover(step Step, attempt int, lastErr error) Decision }

type TrustedExecutor struct {
    Classifier Classifier
    Verifier   Verifier
    Recoverer  Recoverer
    // Injected side effects — agent/ui/llm supply these; trust never imports them.
    Exec     func(ctx context.Context, tool string, params json.RawMessage) (string, error)
    Confirm  func(previewJSON string) bool
    Describe func(tool string, params json.RawMessage) string
    Narrate  func(msg string)
    Replan   func(ctx context.Context, remaining []Step, failed Step, err error) []Step
    Ask      func(step Step, reason string) Decision
}

func (t *TrustedExecutor) Run(ctx context.Context, steps []Step, command string) (Report, error)
```

`Exec` is the seam that runs a tool: `agent` supplies a closure that does the registry lookup,
the existing `{PREVIOUS_OUTPUT}` injection, **and the inner workflow-approval (Phase-3)
handshake**, and folds a `ToolResult`-encoded failure into a non-nil `error` — so `trust` sees a
clean `(result, err)` and never needs to import `tools`. All fields are injectable → the
executor is testable end-to-end with fakes and does no I/O of its own.

---

## Units

### 1. `internal/trust/classify.go` — RiskClassifier (pure)

Decides `Safe` vs `Risky`, no I/O, table-testable. Decision order:

1. **Explicit rule table by tool name** (authoritative):
   - **Risky:** `delete_file`, `write_file`, `create_file`, `keyboard_type`, `keyboard_combo`,
     `native_click`, `mouse_click`, `mouse_move`, `mouse_drag`, `run_python`, `run_terminal`,
     `browser_navigate`, `google_workflow_agent`, `spotify_workflow_agent`, `google_ai`.
   - **Safe:** `get_datetime`, `list_files`, `read_file`, `search`, `screenshot_analysis`,
     `explain_selection`, `recall`, `list_memories`, `browser_read_page`, and read-only
     media/window/system controls.
2. **Fallback to the tool's `RequiresConfirmation()`** when the name is not in the table →
   `Risky` if it returns true.
3. **Param-aware bump** — a normally-safe classification becomes `Risky` when params signal
   danger (pure predicates, no LLM):
   - `write_file`/`create_file` whose target path already exists (overwrite).
   - `system_control` with action in {`shutdown`, `restart`, `logoff`}.
   - `media_control`/`window_control` with action in {`close`, `kill`}.

Unknown tool → consult `RequiresConfirmation()`, else default **Risky** (safe default). The
whole "what is risky" policy is auditable in a single file.

### 2. `internal/trust/preview.go` — Previewer + gate

Gate condition, evaluated in `TrustedExecutor.Run` before any execution:

```
gate = len(plan.Tasks) >= 2  ||  anyStepRisky
```

Safe single step → skip, run immediately. Otherwise build the preview and call
`Confirm(previewJSON)`; on `false`, abort with **zero side effects**.

Preview reuses the confirm-card contract (`title` + `plan.steps[]{label,value}`) so no new UI
wiring is needed:

```json
{ "type":"workflow_approval", "title":"Review this 3-step task",
  "plan": { "goal":"<original command>",
            "steps":[ {"label":"Step 1 · Safe","value":"Search files for 'invoice'"},
                      {"label":"Step 2 · ⚠ Risky","value":"Delete invoice_old.pdf"},
                      {"label":"Step 3 · Safe","value":"Open the invoices folder"} ] } }
```

Each `value` comes from `Describe(tool, params)` — a per-tool formatter map (e.g.
`delete_file{path}` → "Delete invoice_old.pdf"), falling back to the step's `Goal`, then to
`tool(params)`. Risky steps are tagged `⚠ Risky` in the label. Approve = run the whole plan
(the gate was the checkpoint); reject = abort. No per-step re-confirmation during the run.

### 3. `internal/trust/verify.go` — Verifier (cheap-first)

After each step, decide if it worked. First conclusive check wins:

1. **Tool error** — `execErr != nil` → not verified. Free. (The `Exec` closure has already
   folded a `ToolResult`-encoded failure/empty-required signal into `execErr`, so `trust` never
   parses `tools.ToolResult` itself.)
2. **Deterministic post-condition** via `postCheck(tool, params, result) (checked, ok bool)`:
   - `create_file`/`write_file` → target path now exists (non-empty for write).
   - `delete_file` → path no longer exists.
   - `open_file` / app-launch → process or window appeared (best-effort within a short timeout).
   - `get_datetime`/`read_file`/`search` → non-empty result.
   If `checked`, return `ok`. Free / near-free (filesystem + process stat).
3. **LLM judge — fuzzy steps only** (`native_click`, `keyboard_*`, `mouse_*`, vision actions
   with no deterministic check): one small prompt — *"Goal: X. Result/observation: Y. Did it
   succeed? yes/no + reason."* Gated behind a `VerifyWithLLM` capability; if no provider is
   wired the step is treated as verified (never block on an unavailable check).

**Default when nothing applies:** verified = true. The layer acts only on evidence of failure;
it never fabricates failures.

### 4. `internal/trust/recover.go` — Recoverer (pure ladder)

Pure state machine; LLM re-plan and user-ask are injected callbacks on `TrustedExecutor`.

```go
func (r *Recoverer) Recover(step Step, attempt int, lastErr error) Decision
```

Ladder per failing step:

1. **Retry** — up to `maxRetries` (default **2**), no LLM, short backoff between tries.
2. **Replan** — retries exhausted → `Decision = Replan`. `TrustedExecutor` calls
   `Replan(ctx, remaining, failed, err)` **exactly once per plan** (a `replanned` bool guards
   it so a bad re-plan cannot loop-burn tokens); execution continues on the returned tail.
3. **Ask** — re-plan already used (or returns empty) → `Decision = Ask`. `TrustedExecutor` calls
   `Ask(step, err)` → {Retry, Skip, Abort} via a small UI prompt.
4. **Abort** — user says stop, or `Ask` unavailable → `Decision = Abort`.

**Budget guard:** ≤ `maxRetries` retries + **1** re-plan per whole plan. Bounded tokens and
time. **No auto-rollback** of completed steps (unsafe/impossible for most desktop actions); the
final report lists what completed so the user can undo manually.

---

## Integration & wiring

- **`agent.GraphExecutor` gains `Trusted *trust.TrustedExecutor`** (nil-safe). When set,
  `ExecutePlan` converts its `[]Task` → `[]trust.Step` and calls `Trusted.Run(ctx, steps,
  plan.Transcript)`, supplying the `Exec` closure (registry lookup + `{PREVIOUS_OUTPUT}`
  injection + the inner workflow-approval handshake + `ToolResult`-failure→`error` folding).
  When nil it runs the current loop unchanged (non-breaking; existing tests pass). The `Report`
  drives the completed/failed summary.
- **`cmd/app/main.go`** constructs the `TrustedExecutor` once and sets `executor.Trusted`:
  `RiskClassifier` (table), `Verifier` (LLM-judge fn wired to `provider`), `Recoverer`
  (`maxRetries=2`), and the injected fns — `Confirm`/`Ask` → `internal/ui`, `Replan` →
  `provider`, `Describe` → the formatter map, `Narrate` → `ui.ShowNotification`. Because the
  engine and dispatch share this one `GraphExecutor`, wiring it once covers both paths.
- **Live narration** — each step emits a status line to the pill via `Narrate`
  ("Step 2/3 · Deleting invoice_old.pdf…" → "done" / "recovering…"), so non-gated and
  re-planned steps are always visible without extra approvals.
- **New UI glue** — `internal/ui` gains `AskStepChoice(step, reason string) Decision` (reuses
  the confirm card with Retry / Skip / Stop). `RequestConfirmationCard` is reused for the outer
  gate.
- **No `Tool` interface change** — `Describe`/`postCheck`/risk tables live in `internal/trust`
  keyed by existing tool names; the 40-tool registry is untouched.
- **Config:** one flag `"trusted_execution"` (default **true**; off is an escape hatch).
  `privacy_mode` unaffected. LLM-judge and re-plan naturally no-op when no provider/key.

---

## Testing

- **Classifier** (pure) — table tests: each risky/safe tool name; param-bump cases (overwrite
  path exists, `system_control shutdown`, `window_control close`); unknown-tool default.
- **Verifier** — table tests over a temp dir: create→exists, write→non-empty, delete→gone,
  empty-result→fail, tool-error→fail; LLM-judge path with a fake provider (yes/no); default-true
  when no check applies.
- **Recoverer** (pure) — state-machine tests with injected clock + fake replan/ask:
  retry-exhaust→replan, replan-once-only guard, ask→{skip,abort}, budget cap.
- **TrustedExecutor end-to-end** — fake tools + fake Confirm/Verifier/Recoverer: gate fires on
  multi-step; gate fires on single risky; gate skipped on single safe; reject→zero side effects;
  fail→retry→verify→continue; replan tail executes without a second gate; abort produces a
  whole-plan report.
- **Executor delegation** — `Trusted == nil` runs the legacy loop (existing tests unchanged);
  `Trusted != nil` routes through the layer.
- GUI/vision LLM-judge and the real UI prompts are thin glue — verified manually.

---

## Files touched (anticipated)

**New:** `internal/trust/executor.go` (TrustedExecutor.Run + injected fns),
`internal/trust/classify.go`, `internal/trust/preview.go`, `internal/trust/verify.go`,
`internal/trust/recover.go`, plus `*_test.go` alongside each; `internal/ui/ask_step.go`
(`AskStepChoice`).

**Modified:** `internal/agent/executor.go` (`Trusted` field + delegation),
`cmd/app/main.go` (construct + inject the layer), `config/config.go` (`trusted_execution` flag),
`internal/ui/overlay.go` + `internal/ui/overlay_v2.html` (step-choice prompt glue),
`README.md` / `CLAUDE.md` (document the layer).

---

## Non-goals (SP4)

- **Auto-rollback / undo** of completed steps — reported, not reversed.
- **A second gate for re-planned steps** — surfaced via narration, not re-approval.
- **Bringing workflow agents' internal sub-steps under the trust layer** — they keep their
  existing inner approval card; unifying their internals is a later refinement.
- **A new voice/hotkey surface** — SP4 rides the existing command + voice paths.
- **Parallel step execution** — the executor stays sequential.
