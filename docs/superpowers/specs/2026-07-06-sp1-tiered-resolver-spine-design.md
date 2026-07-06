# SP1 — The Spine + Overhead Cut (Design Spec)

**Date:** 2026-07-06
**Status:** Approved (design), pending implementation plan
**Sub-project:** SP1 of 4 (see "Program context" below)

---

## Program context

This spec is the first of four sequenced sub-projects that together turn the Voice Agent
into a **low-overhead, tiered-intelligence PC assistant**. The product thesis:

> Cheap by default, expensive only when needed. A fast **local** tier handles the common
> 80% instantly and offline (zero tokens). A single hotkey gives deep context on whatever
> you're doing. The heavy cloud-LLM orchestration fires only for the hard 20%. Cheap,
> event-driven local triggers make it proactive without burning cycles.

Compute strategy is **cloud-LLM-only (BYOK)** — there is no local model. Therefore the
"fast local tier" is **deterministic Go code**, and "low overhead" means: small binary,
low *idle* CPU/RAM, fast cold start, and the LLM never on the hot path for common actions.

The four sub-projects (each gets its own spec → plan → build cycle):

- **SP1 — The Spine + Overhead Cut** *(this spec)*: the Tier-0 local resolver with
  confidence-gated cloud fallback, plus the dead-code/overhead cut. Foundation for all else.
- **SP2 — Deep-Context Hotkey**: one global hotkey → grab screen/selection/clipboard/app → instant context actions.
- **SP3 — Ambient Trigger Engine**: event-driven proactive suggestions (reuses the dormant `internal/alerts`).
- **SP4 — Trustworthy One-Shot Automation**: workflow-agent reliability, preview/approve UX, wire up the dormant Microsoft suite.

SP1 explicitly keeps SP3/SP4 dormant code in the tree, marked "planned, not wired."

---

## 1. Goal & success criteria

Introduce a **local-first resolution tier** so common commands execute instantly, offline,
with zero tokens — making the cloud LLM a genuine *fallback* rather than the default path.
Simultaneously strip the codebase to a lean base and fix known correctness bugs.

**Success criteria (Definition of Done):**

1. A defined "common commands" test set (~30–40 phrasings across the seven matchers)
   resolves **locally**, in **< 50 ms**, with **0 network calls**.
2. Both **voice and text** inputs flow through **one unified dispatch**. The current
   behavior where every voice command hits the cloud orchestrator is removed for
   locally-resolvable commands.
3. Idle RAM and cold-start time **measurably drop** versus a baseline, and the binary
   shrinks. Hard target numbers are set **after** the baseline is measured (implementation
   task #1) — no numbers are guessed in this spec.
4. The two identified correctness bugs are fixed.
5. Cruft is deleted; `go build ./...` is clean; `go vet ./...` passes; all tests pass.

**Explicit non-goals for SP1:**

- No local ML model (cloud-only stands).
- No new signature *feature* surface (that is SP2–SP4). SP1 makes existing common actions
  instant and the base lean.
- Fallback-LLM-on-error wiring is **out of scope** (noted as a candidate for a later slice).

---

## 2. Architecture: tiered dispatch

```
User input (voice transcript OR typed text)  ── or ── ambient event [SP3, later]
        │
   ┌────▼──────────────────────────────┐
   │ TIER 0 — internal/resolver         │  deterministic, offline, 0 tokens, < 50ms
   │  prioritized chain of Matchers     │  ← handles the common 80%
   │  each returns a confidence score   │
   └────┬───────────────────────────────┘
        │ best confidence < threshold (~0.7)  OR  no match
   ┌────▼──────────────────────────────┐
   │ TIER 1 — existing cloud path       │  the hard 20%
   │  Orchestrator.Run / ClassifyAndPlan│
   └────────────────────────────────────┘
```

### 2.1 New package: `internal/resolver/`

A prioritized chain of deterministic matchers, each scoring its own confidence.

```go
// NormalizedInput is the pre-processed command handed to every matcher.
type NormalizedInput struct {
    Raw       string   // original text
    Lower     string   // lowercased, trimmed
    Tokens    []string // tokenized
    ActiveApp string   // foreground app (from internal/context), may be ""
}

type Match struct {
    Tasks      []agent.Task // one or more tasks to hand to GraphExecutor
    Confidence float64      // 0.0 .. 1.0
    Reason     string       // for the local tier-usage log / debugging
}

type Matcher interface {
    Name() string
    Match(in NormalizedInput) (*Match, bool) // ok=false means "not my intent"
}

// Resolver runs matchers in priority order and returns the first match whose
// confidence >= Threshold. Returns (nil, false) if nothing qualifies.
type Resolver struct {
    matchers  []Matcher
    Threshold float64 // default ~0.7
}
func (r *Resolver) Resolve(in NormalizedInput) (*Match, bool)
```

**Design rationale (isolation & clarity):** each matcher is a self-contained unit with a
single intent domain, testable in isolation via a table of `input → expected (Tasks,
Confidence)`. Adding/removing a capability is adding/removing one matcher — no change to the
resolver core. The resolver core only knows the `Matcher` interface, not any concrete matcher.

### 2.2 Matchers shipped in SP1 (the common 80%)

All are local and instant. Each emits `agent.Task`s targeting **existing registered tools**
where possible; where it wires previously-dormant local logic, that is noted.

| Matcher | Trigger phrasings (examples) | Produces | Backed by |
|---|---|---|---|
| **AppLauncher** | "open/launch/start \<app\>" | `open_app` task | `executor/apps.go` Start-Menu enumeration + fuzzy match |
| **FileFind** | "find/open file \<name\>" | `open_file` / `list_files` | existing `internal/search` indexer |
| **WebOpen / WebSearch** | "open youtube.com", "google \<q\>", "search \<q\>" | `open_website` / `web_search` | existing tools |
| **MediaControl** | "play / pause / next / previous / volume up/down/mute" | local key event | **Windows media keys via `keybd_event`** (no auth, no Spotify dep) — new small local action |
| **SystemToggle** | "lock / sleep / brightness up/down / mute system" | local action | wires the dormant `windows_settings` logic as local actions |
| **WindowControl** | "minimize / maximize / close / snap left/right / switch window" | local Win32 action | Win32 (`user32`) |
| **DateTime** | "what time / what's the date" | answered locally | existing `get_datetime` |

**Ambiguity handling:** if a matcher finds multiple plausible targets (e.g. two installed
apps match "word"), it returns a **reduced confidence** so the input either (a) falls to
Tier 1, or (b) surfaces a disambiguation prompt via the existing `AmbiguousResult`
convention — never a silent guess of `[0]`.

**Confidence scoring guidance:** exact keyword + strong fuzzy target → ~0.9; keyword match
with weak/one-of-many target → ~0.5–0.6 (below threshold → Tier 1). Thresholds are tunable
constants, informed by the local tier-usage log (§2.4).

### 2.3 Unified dispatch & consolidated security

A single dispatch function both input paths call:

```
dispatch(input, source) →
    normalize(input)
    if m, ok := resolver.Resolve(norm); ok {
        enforceSecurity(m.Tasks)         // profile check + confirmation, ONCE, centrally
        GraphExecutor.ExecutePlan(ctx, planFrom(m.Tasks))   // Tier 0
    } else {
        Orchestrator.Run(ctx, input)     // Tier 1 (today's cloud path)
    }
```

- **Voice** (`engine/runtime.go` `EventTranscribed`) and **text**
  (`command/router.go` `ProcessCommand`) both call `dispatch`. This removes the current
  "voice always → Orchestrator" behavior for locally-resolvable commands.
- The **two parallel security/confirmation checks** that exist today (one in
  `engine/runtime.go`, one in `command/router.go`) are consolidated into one central
  `enforceSecurity` used by `dispatch`. The router's fail-open-when-profile-nil path is
  removed in favor of the single enforced path.
- The deterministic `command.Parse` grammar is **subsumed** by the resolver's matchers; its
  canned macro intents that still have value are re-expressed as matchers or kept behind the
  Tier-1 path. (Implementation plan decides per-intent; no behavior is silently dropped.)

### 2.4 Local tier-usage log

A lightweight in-process counter records, per command, which tier handled it and (on Tier 0)
which matcher + confidence. **Local only — no network, no upload.** Purpose: tune thresholds
and see the local-vs-cloud hit ratio. Written to the existing audit DB or an in-memory
counter surfaced on request; the implementation plan picks the cheapest option.

---

## 3. Overhead cut & cleanup (surgical policy)

Git history preserves everything deleted. Policy approved: **surgical**.

### 3.1 DELETE (pure cruft)

- Stale backup snapshots: `*.go.<numbers>` (e.g. `cmd/app/main.go.1922826640484986545`,
  `internal/engine/runtime.go.5602619327809123773`, `internal/command/router.go.8391265684147729430`,
  `internal/tools/registry.go.4179977291475977714`, `internal/security/permissions.go.*`,
  `internal/memory/retriever.go.*`, `internal/ui/overlay.go.*`, and siblings).
- Build/diagnostic logs at root: `build.log`, `build2.log`, `build3.txt`, `build_err.txt`,
  `build_errors.txt`, `build_output.txt`, `build_output_utf8.txt`, `crash.log`, `err.txt`,
  `voice-out.log`, `screen_analysis.txt`.
- Stale author-time HTML-patch scripts in `internal/ui/`: `extract.py`, `inject*.py`,
  `fix_encoding.py`, `fix_palette.py`, `update_ui.py`, `update_cmd.py`, `inject_validator.py`,
  `test_0.js`, `test_1.js` (none are invoked by the Go program).
- Root scratch: `test_uia.go`.
- Generated asset dirs no longer used at runtime: `stitch_assets/`, `stitch_generated_screen/`
  (confirm not referenced before deleting).
- Dead code: `Engine.planExecution` (superseded by the orchestrator route); the duplicate
  `internal/memory/pruner.go` scheduler (the live prune is the inline ticker in `main.go`);
  the `output_overlay.go` stub.

**Guard:** before deleting any `.go` symbol, confirm zero references (grep). Before deleting
asset dirs, confirm no `//go:embed` or runtime path references.

### 3.2 FIX (correctness bugs)

- `internal/llm/openai.go` and `internal/llm/anthropic.go`: `ClassifyAndPlan` uses the
  **planning** system prompt (`buildPlanningPrompt(..., false)`) instead of the
  `classifySystemPrompt`, and detects `needs_screen` via brittle substring matching. Align
  them with Gemini's real classification (proper classify prompt + JSON parse + safe
  `NeedsScreen: true` fallback).
- `internal/tools/spotify_ai.go`: `SpotifySmartRecommendTool.Parameters()` has malformed
  JSON (missing comma) → silently falls back to `{"type":"object"}` in `DumpSchemas`. Fix the
  schema string so the model receives real parameters.

### 3.3 KEEP but mark "planned, not wired" (map to later SPs)

- `internal/tools/microsoft_*.go` → SP4. Add a short header comment: "Dormant — wired in SP4."
- `internal/alerts/` → SP3. Same marking.
- `internal/llm/proxy.go` (`ProxyProvider`) + `Fallback*` config fields → candidate for a
  later slice. Mark as planned; do not wire in SP1; do not delete.
- Unregistered local-capable tools that the resolver will supersede or later wire
  (`terminal`, `vscode`, `vault`, `windows_settings`, `media`, `schedule_task`): keep;
  `windows_settings`/media logic is partially reused by the SystemToggle/MediaControl
  matchers. Mark the rest as planned.

### 3.4 LIGHTEN (overhead)

- **Lazy WebViews:** create the automation overlay and highlight overlay on **first use**,
  not at startup. Reduces idle RAM (two fewer WebView2 instances until needed).
- **Conditional heavy init:** load Whisper context only when `enable_voice` is true; init
  Porcupine only when a wake-word key is configured.
- **Build flags:** add `-ldflags="-s -w"` (strip symbol/debug tables) to the documented
  build command to shrink the ~58 MB binary. (CGO deps — sqlite/whisper/robotgo — set a
  floor; target is a meaningful reduction, not a specific MB, until measured.)
- **Baseline first:** implementation task #1 measures idle RAM, cold-start, and binary size
  so §1 criteria #3 gets concrete before/after numbers.

---

## 4. Testing strategy

- **Per-matcher table tests:** `input → expected Tasks + Confidence bucket` for each of the
  seven matchers, including negative cases (input that must NOT match) and ambiguity cases
  (multiple targets → reduced confidence).
- **Resolver tests:** priority ordering; threshold boundary (just-above vs just-below);
  "no match → (nil,false)".
- **Dispatch/routing tests:** high-confidence resolve → GraphExecutor (Tier 0, no provider
  call — assert via a fake provider that records zero calls); miss → Orchestrator (Tier 1)
  invoked. This is the key regression protecting the "0 network calls for common commands"
  criterion.
- **Cleanup guard:** a test/CI check that `go build ./...` and `go vet ./...` are clean and
  that deleted paths stay gone.
- Follows the repo's existing test style (`internal/tools/research_tool_test.go`).

Tests are written **before** implementation per the repo's TDD practice where practical
(matchers and resolver are pure and ideal for test-first).

---

## 5. Files touched (anticipated)

**New:** `internal/resolver/` (resolver core + one file per matcher + tests), a small
`internal/resolver/dispatch.go` or an addition to `internal/command` for the unified entry.

**Modified:** `internal/engine/runtime.go` (`EventTranscribed` → dispatch),
`internal/command/router.go` (`ProcessCommand` → dispatch; remove fail-open),
`internal/security` usage consolidated, `internal/llm/openai.go`, `internal/llm/anthropic.go`,
`internal/tools/spotify_ai.go`, `cmd/app/main.go` (lazy overlays/conditional init, wire
resolver), build docs in `README.md` / `CLAUDE.md`.

**Deleted:** see §3.1.

---

## 6. Risks & mitigations

- **Behavior regression** (a command that used to reach the LLM now resolves locally and
  wrongly): mitigated by the confidence threshold + ambiguity → fallback, and by the routing
  tests. Threshold is conservative (~0.7) and tunable via the tier-usage log.
- **Deleting something still referenced:** mitigated by the grep/embed guard in §3.1 and the
  clean-build guard test.
- **Fuzzy app matching false positives:** AppLauncher returns reduced confidence on weak/
  multi matches so they fall to Tier 1 or disambiguation rather than launching the wrong app.
- **Windows-only local actions** (media keys, Win32 window control): consistent with the
  app's existing Windows-only posture; no cross-platform requirement introduced.
