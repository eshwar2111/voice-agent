# SP3 — Ambient Trigger Engine + Overlay UI Redesign (Design Spec)

**Date:** 2026-07-09
**Status:** Approved (design), pending implementation plan
**Builds on:** SP1 (tiered resolver), SP2 (ambient context + wake word).

---

## Program context

Third slice of the low-overhead tiered-intelligence assistant. SP3 makes the assistant
**proactive** — it notices things and offers help unprompted — and gives the whole overlay a
**sleek, minimalist visual system**. Two features, one plan:

- **Feature A — Ambient trigger engine:** a lightweight, event-driven framework where local
  event sources emit *suggestions*, a central policy layer keeps them from being annoying, and
  each suggestion is an actionable card (message + one-tap action).
- **Feature B — Overlay UI redesign:** unify the pill and every pop-up under one dark-glass
  design system (the approved mockup at `docs/sp3-ui-mockup.html`), and add the suggestion card.

Compute strategy unchanged (cloud-LLM-only, BYOK). **No LLM in the trigger path** — sources are
event-driven/cheaply-polled; the LLM is only involved if the user *accepts* an action that needs
it (e.g. "explain this error").

---

## Feature A — Ambient trigger engine

### Goal

Surface the right small help at the right moment — "ZIP downloaded, unzip it?", "meeting in 10
min, join?", "link copied, open it?" — as a one-tap suggestion, **off by default**, never
intrusive, near-zero idle overhead.

### Success criteria

1. With `enable_proactive: true`, a matching event produces a suggestion card within a couple
   seconds; accepting it runs the action; dismissing removes it.
2. **Off by default** (`enable_proactive` absent/false → engine never starts; zero background
   watchers, zero mic/CPU/network cost).
3. **Never annoys:** at most one suggestion visible; a duplicate (same `DedupKey`) is never
   shown twice; a global min-gap between suggestions; suppressed while the assistant is busy
   (command/TTS active) or in `privacy_mode`.
4. No LLM call happens merely because a trigger fired.

### Units (`internal/ambient/`)

```go
// Suggestion is one proactive, actionable prompt.
type Suggestion struct {
	Source   string // "downloads" | "calendar" | "clipboard"
	Icon     string // card badge glyph key: "download"|"calendar"|"link"|"warn"
	Title    string // "Download finished"
	Message  string // "report.zip · 14 MB — unzip it here?"
	Action   string // button label: "Unzip"
	DedupKey string // suppress repeats
	Run      func(ctx context.Context) error // executed on accept
}

// Source watches for events and emits Suggestions until ctx is cancelled.
type Source interface {
	Name() string
	Start(ctx context.Context, out chan<- Suggestion)
}

// Engine fans in all sources, applies Policy, and drives the UI one card at a time.
type Engine struct { Sources []Source; Policy Policy; UI Deliverer; Busy func() bool }
func (e *Engine) Run(ctx context.Context)   // started only when enabled

// Policy is the pure, testable "should this be shown now?" gate.
type Policy struct { MinGap time.Duration; seen map[string]time.Time; lastShown time.Time }
func (p *Policy) Allow(s Suggestion, now time.Time, busy bool) bool
```

- **Deliverer** is a tiny interface (`ShowSuggestion(id string, s Suggestion)`) so the engine is
  UI-decoupled and testable with a fake. The real impl calls `internal/ui`.
- The engine holds the currently-shown suggestion by `id`; on accept it runs `Run` in a
  goroutine; on dismiss/timeout it clears. Only one active at a time.

### The three sources

1. **Downloads** — `github.com/fsnotify/fsnotify` (pure-Go, event-driven, no polling) watching
   `%USERPROFILE%\Downloads`. On a *created/finished* file, classify by extension →
   `.zip/.rar/.7z` → "Unzip?" (`Run` = extract with `archive/zip`); image/screenshot → "Open?"
   (`Run` = `executor`/`open_file`); installer (`.exe/.msi`) → "Run?" (confirm-gated). Debounce
   partial writes (`.crdownload`/`.part` ignored; act on final rename).
2. **Calendar** — refactor the dormant `internal/alerts` into a `Source`: same 5-min Google/MS
   poll, but emit a `Suggestion` ("Standup in 10 min", `Run` = open the join link) instead of
   calling the UI directly. Degrades silently when calendars aren't linked.
3. **Clipboard** — poll clipboard (~1.5 s) for *changes*; classify content → URL → "Open?";
   `http`-error / stack-trace shape → "Explain?" (`Run` = route through `dispatch` as an
   ai-request with the text as context); tracking-number pattern → "Track?" (open carrier URL).
   Classifiers are pure and table-tested.

### Delivery flow

```
source.Start → Suggestion ── engine (Policy.Allow?) ──► ui.ShowSuggestion(id, s)
                                                          │  card: title/message + [Action][Dismiss]
                              accept (JS→Go binding) ─────┘
                                 → engine.Accept(id) → go s.Run(ctx)   (may use tools/dispatch/LLM)
                              dismiss / 15s timeout → engine.Dismiss(id)
```

### Config
- New `enable_proactive bool` (default false). Engine started from `main.go` only when true.
- Respect existing `privacy_mode` (true → engine suppressed even if enabled).

---

## Feature B — Overlay UI redesign

### Goal
Replace the current overlay look with one cohesive **dark-glass minimalist system** (approved:
`docs/sp3-ui-mockup.html`) across the pill and every card, and add the suggestion card — so the
new proactive prompts feel native.

### Design system (from the approved mockup)
- **Material:** one glass token — `rgba(20,23,31,0.66)` + `backdrop-filter: blur(22px) saturate(1.35)`,
  hairline border `rgba(255,255,255,0.09)`, `inset 0 1px 0` top-light, soft shadow — identical on
  pill (radius 999px) and cards (radius 18px). Committed **dark-glass** (single visual world; it
  floats over the desktop).
- **Type:** native system stack (`system-ui, "Segoe UI", …`) — no webfont. Tabular numerals for
  the meeting timer; letter-spaced uppercase micro-labels.
- **Accent:** periwinkle `#8ea2ff` for live/primary; `#7fd6a2` for "done"; `#f0817e` for
  destructive confirms only.
- **Motion:** pulse only on listening, sweep only on thinking; `prefers-reduced-motion` disables
  both. Nothing else animates.
- **Action grammar:** buttons safe→bold, primary always rightmost.

### Scope
Rewrite `internal/ui/overlay_v2.html`'s style layer and card markup to this system, **preserving
every JS entry point the Go side calls via `w.Eval`** — verified against `internal/ui/overlay.go`:
`updateUI(state[,text])`, `showCommand()`, `showCard(...)` / `renderContent`, `showConfirm...` /
`resolveConfirm`→`confirmCallback`, `triggerMeetingAlert(...)`, plus the settings/dashboard hooks
(`loadSettings`, `persistSettings`, `openSettings`, `loadIntegrationStatusesDash`) and command
submit (`submitCurrentCommand`→`window.submitCommand`). Behavior and bindings unchanged; only the
visual layer and the states→classes mapping are replaced.

### New: suggestion card
- JS `showSuggestion(id, iconKey, title, message, actionLabel)` renders the card with an
  `[actionLabel]` primary and a `[Dismiss]` ghost; buttons call new bindings
  `suggestionAccept(id)` / `suggestionDismiss(id)`; auto-dismiss after 15 s.
- Go: `internal/ui` gains `ShowSuggestion(id, iconKey, title, message, action string)` (dispatches
  the eval) and exposes `OnSuggestionAccept func(id string)` / `OnSuggestionDismiss func(id string)`
  callbacks bound in `StartOverlay`; the ambient engine wires these.

---

## Non-goals (SP3)
- **Idle/focus source** — deferred (weakest value); the engine's `Source` interface makes it a
  drop-in later.
- No LLM in the trigger detection path.
- No new voice/hotkey surface; suggestions are visual + one-tap (voice-accept is a later add).
- No light-theme overlay variant (committed dark-glass); revisit if wanted later.

---

## Files touched (anticipated)

**New:** `internal/ambient/engine.go`, `internal/ambient/policy.go`, `internal/ambient/suggestion.go`,
`internal/ambient/downloads.go`, `internal/ambient/calendar.go`, `internal/ambient/clipboard.go`,
`internal/ambient/classify.go` (pure clipboard/extension classifiers), plus tests alongside;
`internal/ui/suggestion.go` (Deliverer glue).

**Modified:** `internal/ui/overlay_v2.html` (redesign + suggestion card), `internal/ui/overlay.go`
(`ShowSuggestion` + accept/dismiss callbacks/bindings), `config/config.go` (`enable_proactive`),
`cmd/app/main.go` (start engine when enabled), `internal/alerts/alerts.go` (refactored into the
calendar source, or removed once replaced). `go.mod`/`go.sum` (fsnotify).

---

## Testing
- **Policy** (pure): table tests — dedup by key, min-gap enforcement, busy-suppression,
  disabled/privacy short-circuit, with an injected clock.
- **Classifiers** (pure): extension→action and clipboard-content→action (URL / error / tracking /
  nothing) tables.
- **Engine**: fake `Source` + fake `Deliverer` + fake clock/busy — a suggestion flows to the
  deliverer only when policy allows; accept invokes `Run`; a second identical suggestion is
  suppressed; nothing delivered while busy.
- **Downloads/Calendar/Clipboard** OS glue is thin; verified manually (drop a file in Downloads,
  copy a URL) and via the engine tests with fake sources.
- **UI**: manual — confirm the redesigned pill states + all cards render and the existing Go→JS
  calls still work (command bar, confirm, meeting alert), plus the new suggestion card accept/dismiss.
