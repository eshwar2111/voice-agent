# SP6 Live Activities & Split Island — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the island's activity registry from a display mechanism into a data one — activities that start, persist, update themselves from real sources, and end, with two visible at once via a split island.

**Architecture:** A new dependency-free `internal/island` package holds the `Activity` type, a `Registry` (priority ordering, dismissal, cap, coalescing) and a `Provider` interface. Providers each own one goroutine and emit activities; the registry coalesces and pushes to the UI through an injected `Publish` func, so `internal/island` never imports `internal/ui`. On the JS side `resolve()` gains a second slot and a `wakeUntil` timestamp, and a detached bubble renders beside the pill.

**Tech Stack:** Go 1.26, stdlib only for `internal/island`; vanilla ES modules + CSS for the UI; `node --test` for JS; existing `lxn/win` region code reused unchanged.

**Spec:** `docs/superpowers/specs/2026-07-31-sp6-live-activities-design.md`

## Global Constraints

- **`internal/island` imports only the standard library.** No imports from `agent`, `tools`, `ui`, `llm`, or any third-party package. All coupling flows inward via injected funcs — the pattern `internal/trust` already uses in this repo.
- **No new Go dependencies.** No new `require` lines in `go.mod`.
- **No npm, no CDN, no icon fonts.** The page must work fully offline.
- **`resolve()` in `state.js` remains the sole authority on island geometry.** No other code may call `morphTo` or set width/height/borderRadius. This is the invariant the whole SP5 architecture rests on.
- **The window is never resized after creation.** `SetWindowPos` may only carry `SWP_NOSIZE` after initial placement.
- **The two-phase region publish must survive** — widened union before a morph, exact shape on settle, and the tail publish stays gated behind `if(!morphed)`.
- **`trust.approval` keeps no TTL and fails closed.** It must never auto-deny, never silently approve, never deadlock the executor. This is a safety property.
- **`UpdateActivity` / `EndActivity` keep their exact current signatures** — `agent.job`, `trust.approval` and `ambient.nudge` remain push-driven through them.
- Coalescing: **at most one publish per 250ms**; `Significant` updates and `End` bypass it.
- Registry cap: **8 live activities.**
- Tests use stdlib `testing`, table-driven, no testify — match `internal/trust/classify_test.go`.
- Build: `go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app`
- In Bash, prefix Go commands with `export PATH="$PATH:/c/w64devkit/bin"`.
- JS tests: `node --test <file> <file>` (direct-file form; the bare-directory form has an environment quirk on this machine).
- Commit messages end with: `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`

## File Structure

```
internal/island/            NEW — dependency-free, stdlib only
  activity.go               Activity type, Provider interface, Clock
  registry.go               live map, dismissal, cap, priority ordering, coalescing
  runner.go                 provider supervision: goroutines, panic recovery, backoff
  timers.go                 TimerProvider + the Timers store the timer tool registers into
  meeting.go                MeetingProvider (calendar poll)
  *_test.go                 fake clock + fake publisher; no goroutines in tests

internal/ui/
  activity.go               + PublishActivities([]island.Activity)  (ui may import island; not the reverse)

internal/ui/assets/js/
  state.js                  resolve() gains wakeUntil + slot assignment
  geometry.js               + pairLayout() pure function
  activities.js             timer/meeting render slots; bubble slot renderer
  main.js                   bubble element wiring, dismissal, promote-on-click
internal/ui/assets/css/island.css   #bubble styles
internal/ui/assets/index.html       #bubble element
cmd/app/main.go             wire the registry + providers
internal/tools/productivity.go      timer tool registers into island.Timers
```

---

### Task 1: Activity, Clock, and the Registry core

Pure data and ordering logic. No goroutines, no I/O, no UI. Everything here is directly unit-testable, which is why it comes first.

**Files:**
- Create: `internal/island/activity.go`
- Create: `internal/island/registry.go`
- Create: `internal/island/registry_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `type Activity struct { ID, Kind string; Priority int; Data map[string]any; Started, Ends time.Time; Significant bool }`
  - `type Clock interface { Now() time.Time }`
  - `type Provider interface { Name() string; Run(ctx context.Context, emit func(Activity), end func(id string)) error }`
  - `func NewRegistry(clock Clock, publish func([]Activity)) *Registry`
  - `func (r *Registry) Upsert(a Activity)`, `func (r *Registry) End(id string)`, `func (r *Registry) Dismiss(id string)`
  - `func (r *Registry) Snapshot() []Activity`
  - `const MaxLive = 8`

- [ ] **Step 1: Write the failing test**

```go
// internal/island/registry_test.go
package island

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time      { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestRegistry() (*Registry, *fakeClock, *[][]Activity) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	var pushes [][]Activity
	r := NewRegistry(clk, func(as []Activity) {
		cp := make([]Activity, len(as))
		copy(cp, as)
		pushes = append(pushes, cp)
	})
	return r, clk, &pushes
}

func act(id string, prio int, started time.Time) Activity {
	return Activity{ID: id, Kind: "test", Priority: prio, Started: started}
}

func TestSnapshotOrdersByPriorityDescending(t *testing.T) {
	r, clk, _ := newTestRegistry()
	r.Upsert(act("low", 10, clk.t))
	r.Upsert(act("high", 90, clk.t))
	r.Upsert(act("mid", 50, clk.t))

	got := r.Snapshot()
	want := []string{"high", "mid", "low"}
	if len(got) != 3 {
		t.Fatalf("got %d activities, want 3", len(got))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestSnapshotTiesAreStableByInsertionOrder(t *testing.T) {
	r, clk, _ := newTestRegistry()
	r.Upsert(act("first", 50, clk.t))
	r.Upsert(act("second", 50, clk.t))

	got := r.Snapshot()
	if got[0].ID != "first" || got[1].ID != "second" {
		t.Errorf("tie order = %q,%q — want first,second (stable insertion order, "+
			"so equal-priority activities don't flicker between slots)", got[0].ID, got[1].ID)
	}
}

func TestUpsertReplacesSameID(t *testing.T) {
	r, clk, _ := newTestRegistry()
	a := act("timer", 50, clk.t)
	a.Data = map[string]any{"remaining": 60}
	r.Upsert(a)
	a.Data = map[string]any{"remaining": 59}
	r.Upsert(a)

	got := r.Snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d activities, want 1 (same ID must replace, not append)", len(got))
	}
	if got[0].Data["remaining"] != 59 {
		t.Errorf("Data not replaced: got %v", got[0].Data)
	}
}

func TestEndRemoves(t *testing.T) {
	r, clk, _ := newTestRegistry()
	r.Upsert(act("timer", 50, clk.t))
	r.End("timer")
	if len(r.Snapshot()) != 0 {
		t.Errorf("End did not remove the activity")
	}
	r.End("never-existed") // must not panic
}

// Dismissal is keyed on ID + Started. An update to a still-live instance must
// NOT resurrect it, or dismissing a per-second timer would be useless.
func TestDismissSurvivesUpdatesToSameInstance(t *testing.T) {
	r, clk, _ := newTestRegistry()
	started := clk.t
	r.Upsert(act("timer", 50, started))
	r.Dismiss("timer")

	if len(r.Snapshot()) != 0 {
		t.Fatalf("dismissed activity still visible")
	}

	a := act("timer", 50, started)
	a.Data = map[string]any{"remaining": 42}
	r.Upsert(a) // same Started — still the same instance
	if len(r.Snapshot()) != 0 {
		t.Errorf("an update to a dismissed instance resurrected it")
	}
}

func TestDismissClearsForGenuinelyNewInstance(t *testing.T) {
	r, clk, _ := newTestRegistry()
	started := clk.t
	r.Upsert(act("timer", 50, started))
	r.Dismiss("timer")

	clk.advance(time.Minute)
	r.Upsert(act("timer", 50, clk.t)) // new Started = new instance

	if len(r.Snapshot()) != 1 {
		t.Errorf("a new instance did not clear the dismissal")
	}
}

func TestCapDropsNewActivitiesButAllowsUpdates(t *testing.T) {
	r, clk, _ := newTestRegistry()
	for i := 0; i < MaxLive; i++ {
		r.Upsert(Activity{ID: string(rune('a' + i)), Priority: 1, Started: clk.t})
	}
	if len(r.Snapshot()) != MaxLive {
		t.Fatalf("got %d, want %d", len(r.Snapshot()), MaxLive)
	}

	r.Upsert(Activity{ID: "overflow", Priority: 99, Started: clk.t})
	if len(r.Snapshot()) != MaxLive {
		t.Errorf("cap exceeded: a runaway provider emitting unique IDs must not grow the list")
	}

	// An update to an ALREADY-LIVE activity must still be accepted at the cap,
	// otherwise a full registry would freeze every activity's data.
	upd := Activity{ID: "a", Priority: 1, Started: clk.t, Data: map[string]any{"x": 1}}
	r.Upsert(upd)
	for _, a := range r.Snapshot() {
		if a.ID == "a" && a.Data["x"] == 1 {
			return
		}
	}
	t.Errorf("update to an existing activity was rejected at the cap")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -v
```

Expected: FAIL — `undefined: Registry`, `undefined: NewRegistry`, `undefined: Activity`, `undefined: MaxLive`

- [ ] **Step 3: Write `activity.go`**

```go
// Package island owns live activities: things with a beginning, a middle you
// would glance at, and an end. It imports only the standard library; all
// coupling to the rest of the app flows inward via injected funcs, following
// the pattern internal/trust established.
package island

import (
	"context"
	"time"
)

// Activity is one live thing. Data carries whatever the render slots need;
// island does not interpret it.
type Activity struct {
	ID       string         // stable identity, e.g. "timer.pomodoro"
	Kind     string         // render family: "timer" | "meeting" | "job"
	Priority int            // higher wins the main pill; second wins the bubble
	Data     map[string]any // read by the JS render slots
	Started  time.Time      // instance identity — see dismissal in registry.go
	Ends     time.Time      // zero = open-ended

	// Significant marks an update worth interrupting the user for: it wakes the
	// island out of dormant. The EMITTER decides, not the registry — a timer's
	// per-second tick is not significant, the same timer reaching zero is.
	// Without this distinction the island either twitches every second or never
	// wakes at all. Significant updates also bypass the coalescer.
	Significant bool
}

// Clock is injected so countdown and threshold logic is testable without sleeping.
type Clock interface{ Now() time.Time }

// SystemClock is the real implementation.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// Provider owns one goroutine and one concern. Run returns when the provider is
// finished; it must respect ctx cancellation.
type Provider interface {
	Name() string
	Run(ctx context.Context, emit func(Activity), end func(id string)) error
}
```

- [ ] **Step 4: Write `registry.go`**

```go
package island

import (
	"log"
	"sort"
	"sync"
	"time"
)

// MaxLive caps the registry so a buggy provider emitting unique IDs in a loop
// cannot grow the list unbounded.
const MaxLive = 8

type entry struct {
	act Activity
	seq int // insertion order, for stable tie-breaking
}

// Registry holds what is live and publishes ordered snapshots to the UI.
// Publish is injected, so island never imports internal/ui.
type Registry struct {
	mu      sync.Mutex
	clock   Clock
	publish func([]Activity)

	live map[string]entry
	seq  int

	// dismissed maps an activity ID to the Started of the dismissed instance.
	// Keying on Started is what makes dismissal survive updates: a per-second
	// timer tick carries the same Started, so it stays dismissed, while a
	// genuinely new timer carries a new Started and reappears.
	dismissed map[string]time.Time
}

func NewRegistry(clock Clock, publish func([]Activity)) *Registry {
	return &Registry{
		clock:     clock,
		publish:   publish,
		live:      make(map[string]entry),
		dismissed: make(map[string]time.Time),
	}
}

// Upsert adds or replaces an activity.
func (r *Registry) Upsert(a Activity) {
	r.mu.Lock()
	if d, ok := r.dismissed[a.ID]; ok {
		if d.Equal(a.Started) {
			r.mu.Unlock() // same instance, still dismissed
			return
		}
		delete(r.dismissed, a.ID) // new instance clears the dismissal
	}
	existing, isUpdate := r.live[a.ID]
	if !isUpdate && len(r.live) >= MaxLive {
		r.mu.Unlock()
		log.Printf("[island] registry full (%d), dropping new activity %q", MaxLive, a.ID)
		return
	}
	if isUpdate {
		r.live[a.ID] = entry{act: a, seq: existing.seq}
	} else {
		r.seq++
		r.live[a.ID] = entry{act: a, seq: r.seq}
	}
	r.mu.Unlock()
	r.notify(a.Significant)
}

// End removes an activity. Ending an unknown ID is a no-op.
func (r *Registry) End(id string) {
	r.mu.Lock()
	_, existed := r.live[id]
	delete(r.live, id)
	delete(r.dismissed, id)
	r.mu.Unlock()
	if existed {
		r.notify(true) // terminal: never delayed by coalescing
	}
}

// Dismiss hides an activity from the island. It does NOT stop the underlying
// thing — a dismissed timer keeps running.
func (r *Registry) Dismiss(id string) {
	r.mu.Lock()
	e, ok := r.live[id]
	if ok {
		r.dismissed[id] = e.act.Started
		delete(r.live, id)
	}
	r.mu.Unlock()
	if ok {
		r.notify(true)
	}
}

// Snapshot returns the live activities ordered by priority descending, with
// ties broken by insertion order so equal-priority activities never flicker
// between the pill and the bubble.
func (r *Registry) Snapshot() []Activity {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Activity, 0, len(r.live))
	seqOf := make(map[string]int, len(r.live))
	for id, e := range r.live {
		out = append(out, e.act)
		seqOf[id] = e.seq
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return seqOf[out[i].ID] < seqOf[out[j].ID]
	})
	return out
}

// notify is replaced with coalescing logic in Task 2. For now every change
// publishes immediately.
func (r *Registry) notify(force bool) {
	if r.publish != nil {
		r.publish(r.Snapshot())
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -v
```

Expected: PASS (7 tests)

- [ ] **Step 6: Verify the dependency constraint**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go list -f '{{join .Imports "
"}}' ./internal/island/ | grep -E "\.|/" | grep -v "^[a-z/]*$" || echo "stdlib only - correct"
```

Expected: `stdlib only - correct`. If anything from `github.com/yourname/voice-agent/...` or a third-party module appears, the inward-coupling rule is broken — fix before committing.

- [ ] **Step 7: Commit**

```bash
git add internal/island/
git commit -m "feat(island): Activity, Clock, Provider, and the Registry core

Priority ordering with stable tie-breaking, instance-keyed dismissal that
survives updates, and an 8-activity cap that still accepts updates to
already-live entries. Stdlib only; Publish is injected.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Coalescing

Providers emit at wildly different rates — a timer every second, a transfer potentially at 60Hz. Pushing each emit through the bridge into a JS re-render is the performance bug this design exists to avoid. This is the rule that lets SP9 add a progress-heavy provider without a redesign.

**Files:**
- Modify: `internal/island/registry.go` (replace `notify`, add fields)
- Modify: `internal/island/registry_test.go` (add tests)

**Interfaces:**
- Consumes: `Registry`, `Clock`, `Activity` (Task 1)
- Produces:
  - `const CoalesceWindow = 250 * time.Millisecond`
  - `func (r *Registry) Tick()` — called periodically by the runner; publishes if a deferred change is pending and the window has elapsed

- [ ] **Step 1: Write the failing test**

```go
// append to internal/island/registry_test.go

func TestRoutineUpdatesAreCoalesced(t *testing.T) {
	r, clk, pushes := newTestRegistry()

	r.Upsert(act("timer", 50, clk.t)) // first publish is immediate
	if len(*pushes) != 1 {
		t.Fatalf("first Upsert published %d times, want 1", len(*pushes))
	}

	// Three routine updates inside the window must not publish.
	for i := 0; i < 3; i++ {
		clk.advance(50 * time.Millisecond)
		r.Upsert(act("timer", 50, clk.t.Add(-time.Duration(i+1)*50*time.Millisecond)))
	}
	if len(*pushes) != 1 {
		t.Errorf("routine updates inside the 250ms window published %d times, want 1", len(*pushes))
	}
}

func TestTickPublishesDeferredChangeAfterWindow(t *testing.T) {
	r, clk, pushes := newTestRegistry()
	started := clk.t
	r.Upsert(act("timer", 50, started)) // publish 1

	clk.advance(50 * time.Millisecond)
	a := act("timer", 50, started)
	a.Data = map[string]any{"remaining": 10}
	r.Upsert(a) // deferred

	r.Tick() // still inside the window
	if len(*pushes) != 1 {
		t.Fatalf("Tick published early: %d pushes, want 1", len(*pushes))
	}

	clk.advance(CoalesceWindow)
	r.Tick()
	if len(*pushes) != 2 {
		t.Fatalf("Tick did not flush the deferred change: %d pushes, want 2", len(*pushes))
	}
	if (*pushes)[1][0].Data["remaining"] != 10 {
		t.Errorf("flushed stale data: %v", (*pushes)[1][0].Data)
	}

	// Nothing pending now — Tick must not publish again.
	clk.advance(CoalesceWindow)
	r.Tick()
	if len(*pushes) != 2 {
		t.Errorf("Tick published with nothing pending: %d pushes, want 2", len(*pushes))
	}
}

// A timer reaching zero is the update that matters most. It must never wait
// for the coalescing window.
func TestSignificantUpdateBypassesCoalescing(t *testing.T) {
	r, clk, pushes := newTestRegistry()
	started := clk.t
	r.Upsert(act("timer", 50, started)) // publish 1

	clk.advance(10 * time.Millisecond)
	a := act("timer", 50, started)
	a.Significant = true
	r.Upsert(a)

	if len(*pushes) != 2 {
		t.Errorf("Significant update was coalesced: %d pushes, want 2", len(*pushes))
	}
}

func TestEndBypassesCoalescing(t *testing.T) {
	r, clk, pushes := newTestRegistry()
	r.Upsert(act("timer", 50, clk.t))
	clk.advance(10 * time.Millisecond)
	r.End("timer")

	if len(*pushes) != 2 {
		t.Errorf("End was coalesced: %d pushes, want 2 — a terminal event must never be delayed", len(*pushes))
	}
}

// Data is shared by reference between the caller of emit and whatever Snapshot
// hands the UI. Task 3 has providers emitting from their own goroutines while
// the UI reads snapshots, so a provider reusing a scratch map between ticks
// would be an unsynchronized data race. Copy at the boundary rather than
// relying on every future provider author remembering a contract.
func TestUpsertCopiesDataSoCallerMutationCannotRace(t *testing.T) {
	r, clk, _ := newTestRegistry()
	shared := map[string]any{"remaining": 60}
	a := act("timer", 50, clk.t)
	a.Data = shared
	r.Upsert(a)

	shared["remaining"] = 999 // provider reuses its scratch map

	got := r.Snapshot()
	if got[0].Data["remaining"] != 60 {
		t.Errorf("Snapshot reflected a post-Upsert mutation of the caller's map "+
			"(got %v, want 60) — Data must be copied at the boundary",
			got[0].Data["remaining"])
	}
}

func TestDismissBypassesCoalescing(t *testing.T) {
	r, clk, pushes := newTestRegistry()
	r.Upsert(act("timer", 50, clk.t))
	clk.advance(10 * time.Millisecond)
	r.Dismiss("timer")

	if len(*pushes) != 2 {
		t.Errorf("Dismiss was coalesced: %d pushes, want 2 — the UI must respond to a click immediately", len(*pushes))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -run "Coalesc|Tick|Bypass" -v
```

Expected: FAIL — `undefined: CoalesceWindow`, `r.Tick undefined`, and `TestRoutineUpdatesAreCoalesced` failing because every Upsert currently publishes.

- [ ] **Step 3: Replace `notify` with the coalescing implementation**

Add to the `Registry` struct in `registry.go`:

```go
	lastPush time.Time
	pending  bool
```

Add the constant and replace `notify` entirely:

```go
// CoalesceWindow bounds how often routine updates reach the UI. Significant
// updates and terminal events (End, Dismiss) bypass it.
const CoalesceWindow = 250 * time.Millisecond

// notify publishes, or defers to the next Tick if a routine update lands inside
// the coalescing window. force is set for Significant updates and terminal
// events — a timer hitting zero must never wait 250ms.
func (r *Registry) notify(force bool) {
	r.mu.Lock()
	now := r.clock.Now()
	if !force && !r.lastPush.IsZero() && now.Sub(r.lastPush) < CoalesceWindow {
		r.pending = true
		r.mu.Unlock()
		return
	}
	r.lastPush = now
	r.pending = false
	r.mu.Unlock()
	r.publishSnapshot()
}

// Tick flushes a deferred update once the window has elapsed. The runner calls
// it periodically; tests call it directly, which is why the registry owns no
// timer goroutine of its own.
func (r *Registry) Tick() {
	r.mu.Lock()
	if !r.pending || r.clock.Now().Sub(r.lastPush) < CoalesceWindow {
		r.mu.Unlock()
		return
	}
	r.lastPush = r.clock.Now()
	r.pending = false
	r.mu.Unlock()
	r.publishSnapshot()
}

func (r *Registry) publishSnapshot() {
	if r.publish != nil {
		r.publish(r.Snapshot())
	}
}
```

Note the lock discipline: `notify` and `Tick` release `r.mu` **before** calling `publishSnapshot`, and `Snapshot` takes the lock itself. Publishing under the lock would deadlock the moment a publisher calls back into the registry.

**Also add the `Data` copy** that `TestUpsertCopiesDataSoCallerMutationCannotRace` requires. At the top of `Upsert`, before anything else:

```go
	// Copy Data at the boundary. Providers emit from their own goroutines while
	// the UI reads Snapshot(); a provider reusing a scratch map between ticks
	// would otherwise be an unsynchronized race. Copying here means the registry
	// is safe regardless of provider behavior, instead of depending on every
	// future provider author remembering a contract.
	if a.Data != nil {
		cp := make(map[string]any, len(a.Data))
		for k, v := range a.Data {
			cp[k] = v
		}
		a.Data = cp
	}
```

This is a shallow copy — sufficient here because activity `Data` holds scalars and strings. If a future provider nests a mutable value inside `Data`, it must emit a fresh one; note that in the provider's own doc comment rather than deep-copying every tick.

- [ ] **Step 4: Run all island tests**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -v
```

Expected: PASS (12 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/island/
git commit -m "feat(island): coalesce routine updates, bypass for terminal events

At most one publish per 250ms. Significant updates, End and Dismiss bypass it
so a timer hitting zero is never delayed. Tick() is caller-driven so the
registry owns no timer goroutine and the behavior is testable with a fake clock.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: Provider runner

Supervises providers: one goroutine each, panic recovery, error backoff, context cancellation, and the periodic `Tick` that flushes coalesced updates. One bad feed must never take down the island or the agent.

**Files:**
- Create: `internal/island/runner.go`
- Create: `internal/island/runner_test.go`

**Interfaces:**
- Consumes: `Registry`, `Provider`, `Clock` (Tasks 1–2)
- Produces:
  - `func NewRunner(reg *Registry, providers ...Provider) *Runner`
  - `func (rn *Runner) Start(ctx context.Context)` — non-blocking; spawns one goroutine per provider plus a ticker
  - `func (rn *Runner) runOne(ctx context.Context, p Provider)` — exported for tests via the package boundary

- [ ] **Step 1: Write the failing test**

```go
// internal/island/runner_test.go
package island

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type stubProvider struct {
	name  string
	mu    sync.Mutex
	runs  int
	panic bool
	err   error
	emit  *Activity
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Run(ctx context.Context, emit func(Activity), end func(string)) error {
	s.mu.Lock()
	s.runs++
	shouldPanic, err, a := s.panic, s.err, s.emit
	s.mu.Unlock()

	if shouldPanic {
		panic("provider exploded")
	}
	if a != nil {
		emit(*a)
	}
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (s *stubProvider) runCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}

// A panicking provider must not take down the process or its siblings.
func TestRunnerRecoversPanickingProvider(t *testing.T) {
	r, clk, _ := newTestRegistry()
	bad := &stubProvider{name: "bad", panic: true}
	good := &stubProvider{name: "good", emit: &Activity{ID: "ok", Priority: 1, Started: clk.t}}

	rn := NewRunner(r, bad, good)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rn.Start(ctx)

	waitFor(t, func() bool { return len(r.Snapshot()) == 1 })

	if got := r.Snapshot(); len(got) != 1 || got[0].ID != "ok" {
		t.Errorf("sibling provider did not survive the panic: %v", got)
	}
}

// A provider that errors is retried, so a transient API outage does not
// permanently kill an activity.
func TestRunnerRetriesFailingProvider(t *testing.T) {
	r, _, _ := newTestRegistry()
	p := &stubProvider{name: "flaky", err: errors.New("api down")}

	rn := NewRunner(r, p)
	rn.backoff = time.Millisecond // keep the test fast
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rn.Start(ctx)

	waitFor(t, func() bool { return p.runCount() >= 3 })
}

func TestRunnerStopsOnContextCancel(t *testing.T) {
	r, _, _ := newTestRegistry()
	p := &stubProvider{name: "blocker"}

	rn := NewRunner(r, p)
	ctx, cancel := context.WithCancel(context.Background())
	rn.Start(ctx)
	waitFor(t, func() bool { return p.runCount() == 1 })

	cancel()
	// Must not retry after cancellation.
	time.Sleep(20 * time.Millisecond)
	if p.runCount() != 1 {
		t.Errorf("provider restarted after context cancel: %d runs", p.runCount())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within 2s")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -run TestRunner -v
```

Expected: FAIL — `undefined: NewRunner`

- [ ] **Step 3: Write `runner.go`**

```go
package island

import (
	"context"
	"log"
	"time"
)

// Runner supervises providers. Each gets its own goroutine, and a crash or
// error in one must never affect the others or the host process.
type Runner struct {
	reg       *Registry
	providers []Provider

	// backoff is the delay before restarting a provider that returned an error
	// or panicked. Overridden in tests.
	backoff time.Duration
	// tickEvery drives Registry.Tick, which flushes coalesced updates.
	tickEvery time.Duration
}

func NewRunner(reg *Registry, providers ...Provider) *Runner {
	return &Runner{
		reg:       reg,
		providers: providers,
		backoff:   5 * time.Second,
		tickEvery: CoalesceWindow,
	}
}

// Start spawns one goroutine per provider plus the coalescing ticker. It does
// not block.
func (rn *Runner) Start(ctx context.Context) {
	for _, p := range rn.providers {
		go rn.supervise(ctx, p)
	}
	go func() {
		t := time.NewTicker(rn.tickEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rn.reg.Tick()
			}
		}
	}()
}

func (rn *Runner) supervise(ctx context.Context, p Provider) {
	for {
		if ctx.Err() != nil {
			return
		}
		err := rn.runOnce(ctx, p)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[island] provider %s stopped: %v — retrying in %s", p.Name(), err, rn.backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(rn.backoff):
		}
	}
}

// runOnce isolates a single provider run so a panic becomes an error instead of
// a process crash. One bad feed must not kill the island or the agent.
func (rn *Runner) runOnce(ctx context.Context, p Provider) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[island] provider %s panicked: %v", p.Name(), rec)
			err = errPanicked
		}
	}()
	return p.Run(ctx, rn.reg.Upsert, rn.reg.End)
}

var errPanicked = errorString("provider panicked")

type errorString string

func (e errorString) Error() string { return string(e) }
```

- [ ] **Step 4: Run all island tests**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -v -race
```

Expected: PASS (15 tests), no race warnings. The `-race` run matters here — the runner is the first code in this package with real concurrency.

- [ ] **Step 5: Commit**

```bash
git add internal/island/
git commit -m "feat(island): provider runner with panic recovery and backoff

One goroutine per provider; a panic is recovered into an error and the provider
restarts after a backoff, so one bad feed cannot take down the island or its
siblings. Also drives Registry.Tick to flush coalesced updates.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: Timer provider

The timer tool currently fires a fire-and-forget goroutine that sleeps and then calls `ui.ShowOutputOverlay` — there is no state to poll. So the provider owns a small store the tool registers into, and emits ticks from it.

**Files:**
- Create: `internal/island/timers.go`
- Create: `internal/island/timers_test.go`
- Modify: `internal/tools/productivity.go` (register the timer, and cancel on completion)

**Interfaces:**
- Consumes: `Activity`, `Provider`, `Clock`, `Registry` (Tasks 1–3)
- Produces:
  - `func NewTimers(clock Clock) *Timers`
  - `func (t *Timers) Add(id, label string, endsAt time.Time)`
  - `func (t *Timers) Remove(id string)`
  - `*Timers` implements `Provider` (`Name`, `Run`)
  - `var DefaultTimers *Timers` — package-level so the tool layer can register without an import cycle

- [ ] **Step 1: Write the failing test**

```go
// internal/island/timers_test.go
package island

import (
	"testing"
	"time"
)

func TestTimersSnapshotProducesActivity(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(90*time.Second))

	got := tm.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d activities, want 1", len(got))
	}
	a := got[0]
	if a.ID != "timer.t1" {
		t.Errorf("ID = %q, want timer.t1", a.ID)
	}
	if a.Kind != "timer" {
		t.Errorf("Kind = %q, want timer", a.Kind)
	}
	if a.Data["label"] != "tea" {
		t.Errorf("label = %v, want tea", a.Data["label"])
	}
	if a.Data["remaining"] != 90 {
		t.Errorf("remaining = %v, want 90 (seconds)", a.Data["remaining"])
	}
	if a.Significant {
		t.Errorf("a routine tick must NOT be Significant, or the island twitches every second")
	}
}

func TestTimersMarksZeroSignificant(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(2*time.Second))

	clk.advance(2 * time.Second)
	got := tm.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d activities, want 1", len(got))
	}
	if got[0].Data["remaining"] != 0 {
		t.Errorf("remaining = %v, want 0", got[0].Data["remaining"])
	}
	if !got[0].Significant {
		t.Errorf("reaching zero MUST be Significant — it is the update that matters most")
	}
}

func TestTimersRemainingNeverNegative(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(time.Second))

	clk.advance(30 * time.Second) // long overdue
	got := tm.snapshot()
	if got[0].Data["remaining"] != 0 {
		t.Errorf("remaining = %v, want 0 — a countdown must never render negative", got[0].Data["remaining"])
	}
}

func TestTimersRemoveDropsIt(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(time.Minute))
	tm.Remove("t1")
	if len(tm.snapshot()) != 0 {
		t.Errorf("Remove did not drop the timer")
	}
}

// Started must be stable across ticks, or dismissal (keyed on ID+Started)
// would clear itself every second.
func TestTimersStartedIsStableAcrossTicks(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(time.Minute))

	first := tm.snapshot()[0].Started
	clk.advance(3 * time.Second)
	second := tm.snapshot()[0].Started

	if !first.Equal(second) {
		t.Errorf("Started changed between ticks (%v -> %v) — dismissal would reset every tick", first, second)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -run TestTimers -v
```

Expected: FAIL — `undefined: NewTimers`

- [ ] **Step 3: Write `timers.go`**

```go
package island

import (
	"context"
	"sync"
	"time"
)

// DefaultTimers is the process-wide timer store. The tool layer registers into
// it; wiring it as a package var avoids an import cycle, since internal/island
// may not import internal/tools and vice versa would be circular.
var DefaultTimers = NewTimers(SystemClock{})

type timerEntry struct {
	label   string
	started time.Time
	endsAt  time.Time
}

// Timers is both a store the tool layer writes into and a Provider the runner
// supervises.
type Timers struct {
	mu    sync.Mutex
	clock Clock
	items map[string]timerEntry
}

func NewTimers(clock Clock) *Timers {
	return &Timers{clock: clock, items: make(map[string]timerEntry)}
}

func (t *Timers) Add(id, label string, endsAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items[id] = timerEntry{label: label, started: t.clock.Now(), endsAt: endsAt}
}

func (t *Timers) Remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.items, id)
}

func (t *Timers) Name() string { return "timers" }

// snapshot converts the store into activities. Kept separate from Run so the
// conversion is testable without goroutines.
func (t *Timers) snapshot() []Activity {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	out := make([]Activity, 0, len(t.items))
	for id, e := range t.items {
		remaining := int(e.endsAt.Sub(now).Seconds())
		if remaining < 0 {
			remaining = 0 // a countdown must never render negative
		}
		out = append(out, Activity{
			ID:       "timer." + id,
			Kind:     "timer",
			Priority: 60,
			Data: map[string]any{
				"label":     e.label,
				"remaining": remaining,
				"total":     int(e.endsAt.Sub(e.started).Seconds()),
			},
			// Started must be the timer's own start, NOT time.Now(): dismissal
			// is keyed on ID+Started, so a moving Started would clear the
			// dismissal on every tick.
			Started: e.started,
			Ends:    e.endsAt,
			// Only the moment it reaches zero is worth waking the island for.
			Significant: remaining == 0,
		})
	}
	return out
}

// Run ticks once a second and emits every live timer.
func (t *Timers) Run(ctx context.Context, emit func(Activity), end func(string)) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	seen := make(map[string]bool)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			current := make(map[string]bool)
			for _, a := range t.snapshot() {
				emit(a)
				current[a.ID] = true
			}
			for id := range seen {
				if !current[id] {
					end(id)
				}
			}
			seen = current
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -run TestTimers -v
```

Expected: PASS (5 tests)

- [ ] **Step 5: Register timers from the tool**

In `internal/tools/productivity.go`, inside `TimerTool.Execute`, register with the island before starting the sleep goroutine and remove it when the timer fires. The existing `ui.ShowOutputOverlay` call stays — the island shows the countdown, the overlay still announces completion.

```go
	// (inside Execute, after params are parsed and `msg` is set)
	timerID := fmt.Sprintf("%d", time.Now().UnixNano())
	island.DefaultTimers.Add(timerID, msg, time.Now().Add(time.Duration(params.Seconds)*time.Second))

	go func(id string, duration int, message string) {
		time.Sleep(time.Duration(duration) * time.Second)
		island.DefaultTimers.Remove(id)
		ui.ShowOutputOverlay(fmt.Sprintf("⏰ Timer Complete: %s", message))
	}(timerID, params.Seconds, msg)
```

Add `"github.com/yourname/voice-agent/internal/island"` to the imports. Adjust the field name if `TimerArgs` uses something other than `Seconds` — read the struct rather than assuming.

- [ ] **Step 6: Verify the whole repo still builds**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build ./... && go test ./internal/island/ ./internal/tools/
```

Expected: build clean, tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/island/ internal/tools/productivity.go
git commit -m "feat(island): timer provider, registered from the timer tool

The timer tool was fire-and-forget with no state to poll, so Timers is both a
store the tool writes into and a Provider that emits ticks. Started is the
timer's own start so dismissal survives ticks; only reaching zero is Significant.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: Meeting provider

Polls Google Calendar for the next event and emits a countdown. Significant only at the thresholds that matter, so it swells exactly when you need to move.

**Files:**
- Create: `internal/island/meeting.go`
- Create: `internal/island/meeting_test.go`

**Interfaces:**
- Consumes: `Activity`, `Provider`, `Clock` (Tasks 1–3)
- Produces:
  - `type NextMeeting struct { Title, JoinURL string; StartsAt time.Time }`
  - `type MeetingSource func(ctx context.Context) (*NextMeeting, error)`
  - `func NewMeetingProvider(clock Clock, src MeetingSource) *MeetingProvider`
  - `func (m *MeetingProvider) activityFor(n *NextMeeting) (Activity, bool)`

The calendar call is injected as a `MeetingSource` func, so `internal/island` stays stdlib-only and the provider is testable without network access.

- [ ] **Step 1: Write the failing test**

```go
// internal/island/meeting_test.go
package island

import (
	"testing"
	"time"
)

func TestMeetingActivityFields(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	a, ok := m.activityFor(&NextMeeting{
		Title:    "Standup",
		JoinURL:  "https://meet.example/abc",
		StartsAt: clk.t.Add(5 * time.Minute),
	})
	if !ok {
		t.Fatal("activityFor returned ok=false for a valid upcoming meeting")
	}
	if a.ID != "meeting.next" {
		t.Errorf("ID = %q, want meeting.next", a.ID)
	}
	if a.Data["title"] != "Standup" {
		t.Errorf("title = %v", a.Data["title"])
	}
	if a.Data["minutes"] != 5 {
		t.Errorf("minutes = %v, want 5", a.Data["minutes"])
	}
	if a.Data["joinURL"] != "https://meet.example/abc" {
		t.Errorf("joinURL = %v", a.Data["joinURL"])
	}
}

func TestMeetingSignificantOnlyAtThresholds(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	cases := []struct {
		minutes int
		want    bool
	}{
		{30, false},
		{11, false},
		{5, true},  // T-5m
		{4, false}, // already woke at 5
		{1, true},  // T-1m
		{0, true},  // starting now
	}
	for _, tc := range cases {
		m.lastWake = -1 // reset between cases
		a, ok := m.activityFor(&NextMeeting{
			Title: "Standup", StartsAt: clk.t.Add(time.Duration(tc.minutes) * time.Minute),
		})
		if !ok {
			t.Fatalf("%dm: activityFor returned ok=false", tc.minutes)
		}
		if a.Significant != tc.want {
			t.Errorf("%dm: Significant = %v, want %v", tc.minutes, a.Significant, tc.want)
		}
	}
}

// Repeated polls at the same threshold must wake only once, or a 60s poll would
// re-wake the island every minute at T-5.
func TestMeetingWakesOncePerThreshold(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)
	n := &NextMeeting{Title: "Standup", StartsAt: clk.t.Add(5 * time.Minute)}

	first, _ := m.activityFor(n)
	second, _ := m.activityFor(n)

	if !first.Significant {
		t.Errorf("first crossing of T-5m should be Significant")
	}
	if second.Significant {
		t.Errorf("second poll at the same threshold must NOT re-wake the island")
	}
}

func TestMeetingIgnoresPastAndFarFuture(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	if _, ok := m.activityFor(nil); ok {
		t.Errorf("nil meeting produced an activity")
	}
	if _, ok := m.activityFor(&NextMeeting{Title: "Old", StartsAt: clk.t.Add(-time.Hour)}); ok {
		t.Errorf("a meeting an hour in the past produced an activity")
	}
	if _, ok := m.activityFor(&NextMeeting{Title: "Later", StartsAt: clk.t.Add(3 * time.Hour)}); ok {
		t.Errorf("a meeting 3h away produced an activity — the island is not a calendar")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -run TestMeeting -v
```

Expected: FAIL — `undefined: NewMeetingProvider`

- [ ] **Step 3: Write `meeting.go`**

```go
package island

import (
	"context"
	"time"
)

// LookaheadMinutes bounds how early a meeting appears. The island shows what is
// imminent; it is not a calendar.
const LookaheadMinutes = 60

// wakeThresholds are the remaining-minute values worth interrupting for.
var wakeThresholds = []int{5, 1, 0}

type NextMeeting struct {
	Title    string
	JoinURL  string
	StartsAt time.Time
}

// MeetingSource fetches the next meeting. Injected so internal/island stays
// stdlib-only and the provider is testable without network access.
type MeetingSource func(ctx context.Context) (*NextMeeting, error)

type MeetingProvider struct {
	clock Clock
	src   MeetingSource
	// lastWake is the threshold already woken for, so a 60s poll does not
	// re-wake the island every minute at the same threshold.
	lastWake int
	started  time.Time
}

func NewMeetingProvider(clock Clock, src MeetingSource) *MeetingProvider {
	return &MeetingProvider{clock: clock, src: src, lastWake: -1}
}

func (m *MeetingProvider) Name() string { return "meeting" }

func (m *MeetingProvider) activityFor(n *NextMeeting) (Activity, bool) {
	if n == nil {
		return Activity{}, false
	}
	now := m.clock.Now()
	mins := int(n.StartsAt.Sub(now).Minutes())
	if mins < 0 || mins > LookaheadMinutes {
		return Activity{}, false
	}

	significant := false
	for _, th := range wakeThresholds {
		if mins <= th && m.lastWake != th {
			significant = true
			m.lastWake = th
			break
		}
	}

	// Started identifies the instance for dismissal; use the meeting's own
	// start so it stays stable across polls.
	if m.started.IsZero() {
		m.started = n.StartsAt
	}

	return Activity{
		ID:       "meeting.next",
		Kind:     "meeting",
		Priority: 70,
		Data: map[string]any{
			"title":   n.Title,
			"minutes": mins,
			"joinURL": n.JoinURL,
		},
		Started:     n.StartsAt,
		Ends:        n.StartsAt,
		Significant: significant,
	}, true
}

// Run polls once a minute.
func (m *MeetingProvider) Run(ctx context.Context, emit func(Activity), end func(string)) error {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	live := false
	poll := func() error {
		if m.src == nil {
			return nil
		}
		n, err := m.src(ctx)
		if err != nil {
			return err
		}
		a, ok := m.activityFor(n)
		if ok {
			emit(a)
			live = true
		} else if live {
			end("meeting.next")
			live = false
			m.lastWake = -1
		}
		return nil
	}
	if err := poll(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := poll(); err != nil {
				return err
			}
		}
	}
}
```

- [ ] **Step 4: Run all island tests**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/island/ -v -race
```

Expected: PASS (24 tests), no races.

- [ ] **Step 5: Commit**

```bash
git add internal/island/
git commit -m "feat(island): meeting provider with threshold-based waking

Polls an injected MeetingSource once a minute and emits a countdown. Significant
only when crossing T-5m/T-1m/start, and only once per threshold, so a 60s poll
does not re-wake the island every minute. 60m lookahead - the island shows what
is imminent, not a calendar.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: Go → JS transport

Carries registry snapshots to the page. The existing push path (`UpdateActivity`/`EndActivity`) must keep working unchanged alongside it.

**Files:**
- Modify: `internal/ui/activity.go`
- Modify: `internal/ui/assets/js/main.js`
- Modify: `internal/ui/assets/js/activities.js`

**Interfaces:**
- Consumes: `island.Activity` (Task 1); `Bridge.Push` (SP5)
- Produces:
  - Go: `func PublishActivities(as []island.Activity)`
  - JS: `window.__agent.on('activity:sync', …)`, and `syncProviderActivities(list)` in `activities.js`

- [ ] **Step 1: Add the Go publisher**

`internal/ui` may import `internal/island` — the dependency runs outward-in, and only the reverse is forbidden.

```go
// append to internal/ui/activity.go

// PublishActivities replaces the set of provider-driven activities in the
// island. It is the Publish func injected into island.Registry.
//
// This does NOT disturb push-driven activities (agent.job, trust.approval,
// ambient.nudge) — the JS side keeps the two sets separate and merges them for
// display, so a provider snapshot can never clear a pending approval.
func PublishActivities(as []island.Activity) {
	if bridge == nil {
		return
	}
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		out = append(out, map[string]any{
			"id":          a.ID,
			"kind":        a.Kind,
			"priority":    a.Priority,
			"data":        a.Data,
			"significant": a.Significant,
		})
	}
	bridge.Push("activity:sync", map[string]any{"activities": out})
}
```

Add `"github.com/yourname/voice-agent/internal/island"` to the imports.

- [ ] **Step 2: Add the JS ingestion path**

In `activities.js`, add a second store for provider activities, kept separate from the push-driven `live` map:

```js
// Provider-driven activities arrive as a full snapshot and REPLACE this set.
// They are deliberately separate from `live` (push-driven): a provider snapshot
// must never be able to clear a pending trust.approval.
const provided = new Map(); // id -> { data, priority, kind, significant }

export function syncProviderActivities(list, onChange){
  provided.clear();
  for(const a of (list || [])){
    if(!a || !a.id) continue;
    provided.set(a.id, { data: a.data || {}, priority: a.priority|0,
                         kind: a.kind || '', significant: !!a.significant });
  }
  onChange && onChange();
}

// Any provider activity marked significant in the latest snapshot.
export function hasSignificantUpdate(){
  for(const [, v] of provided) if(v.significant) return true;
  return false;
}

// kindRenderers is populated in Task 9 (timer + meeting slots). Declare it
// empty here so this task compiles and runs standalone — renderProvided simply
// returns null until Task 9 fills it in.
export const kindRenderers = {};

export function renderProvided(id, slot){
  const v = provided.get(id);
  if(!v) return null;
  const r = kindRenderers[v.kind];
  return r && r[slot] ? r[slot](v.data) : null;
}
```

Also extend `activeActivities()` to return the union of `live` and `provided`, each entry as `{id, priority}` exactly as today, so `resolve()` needs no changes to consume provider activities.

**This is also what gives `agent.job` its bubble eligibility** (spec §4). `agent.job` stays push-driven in `live`; because `activeActivities()` now returns the merged, priority-sorted union and Task 7 assigns the second entry to the bubble, a long agent job automatically becomes bubble-eligible with no further work. No separate task is needed for it.

Extend `activeActivities()` so it returns the union of both sets, each entry carrying `{id, priority}` as it does today. Provider entries also carry `kind` so `main.js` can pick the right renderer.

- [ ] **Step 3: Wire the bridge handler in `main.js`**

```js
window.__agent.on('activity:sync', d => {
  syncProviderActivities(d.activities, () => {
    if(hasSignificantUpdate()) store.wakeUntil = Date.now() + WAKE_MS;
    syncAndRerender();
  });
});
```

`WAKE_MS` is defined in Task 7. Import `syncProviderActivities` and `hasSignificantUpdate` from `activities.js`.

- [ ] **Step 4: Verify the build and the existing suites**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build ./... && go test ./internal/... && node --test internal/ui/assets/js/state.test.js internal/ui/assets/js/geometry.test.js internal/ui/assets/js/activities.test.js internal/ui/assets/js/motion.test.js internal/ui/assets/js/surfaces.test.js
```

Expected: build clean; all Go tests pass; 34 JS tests still pass. Nothing in this step should change existing behavior.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/activity.go internal/ui/assets/js/
git commit -m "feat(ui): activity:sync transport for provider-driven activities

Registry snapshots reach the page as a full replace, kept in a separate store
from push-driven activities so a snapshot can never clear a pending approval.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: Two-slot resolution and wake-on-change

`resolve()` gains a second slot and a wake timestamp, and stays the sole authority on geometry.

**Files:**
- Modify: `internal/ui/assets/js/state.js`
- Modify: `internal/ui/assets/js/state.test.js`

**Interfaces:**
- Consumes: `resolve(store)`, `topActivity(activities)`, `PRESENCE_SIZES` (SP5)
- Produces:
  - `export const WAKE_MS = 2500`
  - `resolve(store)` now returns `{presence, contentId, bubbleId, surface}`

- [ ] **Step 1: Write the failing test**

```js
// append to internal/ui/assets/js/state.test.js
import { WAKE_MS } from './state.js';

const base2 = {
  surface: null, activities: [], agentState: 'idle',
  hover: false, idleSince: 0, now: 0, wakeUntil: 0,
};
const s2 = (o) => ({ ...base2, ...o });

test('second activity goes to the bubble slot', () => {
  const r = resolve(s2({ activities: [
    { id: 'agent.job', priority: 90 },
    { id: 'timer.t1',  priority: 60 },
  ]}));
  assert.equal(r.contentId, 'agent.job');
  assert.equal(r.bubbleId, 'timer.t1');
});

test('a third activity is live but not rendered', () => {
  const r = resolve(s2({ activities: [
    { id: 'a', priority: 90 }, { id: 'b', priority: 60 }, { id: 'c', priority: 10 },
  ]}));
  assert.equal(r.contentId, 'a');
  assert.equal(r.bubbleId, 'b');
  assert.ok(r.bubbleId !== 'c');
});

test('a single activity leaves the bubble empty', () => {
  const r = resolve(s2({ activities: [{ id: 'timer.t1', priority: 60 }] }));
  assert.equal(r.bubbleId, null);
});

test('an approval takes the pill and demotes music to the bubble', () => {
  const r = resolve(s2({ activities: [
    { id: 'spotify.nowplaying', priority: 20 },
    { id: 'trust.approval',     priority: 100 },
  ]}));
  assert.equal(r.contentId, 'trust.approval');
  assert.equal(r.presence, 'expanded');
  assert.equal(r.bubbleId, 'spotify.nowplaying');
});

test('wakeUntil lifts a dormant island to peek', () => {
  const dormant = s2({ now: 60000, idleSince: 0,
                       activities: [{ id: 'timer.t1', priority: 60 }] });
  assert.equal(resolve(dormant).presence, 'compact');

  const woken = { ...dormant, wakeUntil: 60000 + 1000 };
  assert.equal(resolve(woken).presence, 'peek');
});

test('an expired wakeUntil does not hold the island open', () => {
  const r = resolve(s2({ now: 60000, wakeUntil: 59000,
                         activities: [{ id: 'timer.t1', priority: 60 }] }));
  assert.equal(r.presence, 'compact');
});

test('wakeUntil never overrides an open surface', () => {
  const r = resolve(s2({ surface: 'command', now: 1000, wakeUntil: 9999 }));
  assert.equal(r.presence, 'sheet');
  assert.equal(r.contentId, 'command');
});

test('WAKE_MS is a sane wake duration', () => {
  assert.ok(WAKE_MS >= 1500 && WAKE_MS <= 4000);
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
node --test internal/ui/assets/js/state.test.js
```

Expected: FAIL — `WAKE_MS` is not exported, and `r.bubbleId` is `undefined`.

- [ ] **Step 3: Update `state.js`**

Add the constant:

```js
// How long a significant update holds the island at peek before it recedes.
export const WAKE_MS = 2500;
```

In `resolve()`, after the existing surface and approval branches, compute the bubble from the second-ranked activity and apply the wake. The ordering rules that must hold:

1. An open surface still outranks everything (unchanged).
2. `bubbleId` is the **second** entry of the priority-sorted activity list, or `null`.
3. `wakeUntil > now` lifts `dormant` → `peek`, and lifts `compact` → `peek`. It must **never** downgrade a larger presence, and never override an open surface or an approval's `expanded`.

Concretely: sort activities by priority descending (reuse the same comparison `topActivity` uses), take `[0]` for `contentId` and `[1]?.id ?? null` for `bubbleId`. Then, immediately before returning in the idle and single-activity branches only, apply:

```js
  const woken = store.wakeUntil && store.now < store.wakeUntil;
  if (woken && (presence === 'dormant' || presence === 'compact')) presence = 'peek';
```

Keep `resolve()` pure — no DOM, no `Date.now()`; the caller supplies `now`.

- [ ] **Step 4: Run tests to verify they pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
node --test internal/ui/assets/js/state.test.js
```

Expected: PASS (17 tests — the 9 existing plus 8 new)

- [ ] **Step 5: Commit**

```bash
git add internal/ui/assets/js/state.js internal/ui/assets/js/state.test.js
git commit -m "feat(ui): two-slot resolution and wake-on-change

resolve() now returns bubbleId for the second-ranked activity, and a wakeUntil
timestamp lifts dormant/compact to peek without ever downgrading a larger
presence or overriding an open surface. resolve() stays pure and stays the sole
geometry authority.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 8: Pair-centering geometry

When a bubble appears the pill must shift left so the **pair** stays centered. Without it, every activity that starts visibly shoves the island sideways. Pure math, so it lives in `geometry.js` next to `unionIslandRect` — the split that made SP5's region math testable after it caused a defect.

**Files:**
- Modify: `internal/ui/assets/js/geometry.js`
- Create: `internal/ui/assets/js/geometry.test.js` additions

**Interfaces:**
- Consumes: `PRESENCE_SIZES` (state.js)
- Produces:
  - `export const BUBBLE = { compact: 32, peek: 44, gap: 8 }`
  - `export function pairLayout(presence, hasBubble, canvasWidth)` → `{ pillLeft, bubbleLeft, bubbleSize }`

- [ ] **Step 1: Write the failing test**

```js
// append to internal/ui/assets/js/geometry.test.js
import { pairLayout, BUBBLE } from './geometry.js';
import { PRESENCE_SIZES } from './state.js';

test('without a bubble the pill is plain-centered', () => {
  const { pillLeft, bubbleLeft, bubbleSize } = pairLayout('compact', false, 1200);
  assert.equal(pillLeft, (1200 - PRESENCE_SIZES.compact.w) / 2);
  assert.equal(bubbleLeft, null);
  assert.equal(bubbleSize, 0);
});

test('with a bubble the PAIR is centered, not the pill', () => {
  const w = PRESENCE_SIZES.compact.w;
  const total = w + BUBBLE.gap + BUBBLE.compact;
  const { pillLeft, bubbleLeft, bubbleSize } = pairLayout('compact', true, 1200);

  assert.equal(bubbleSize, BUBBLE.compact);
  assert.equal(pillLeft, (1200 - total) / 2);
  assert.equal(bubbleLeft, pillLeft + w + BUBBLE.gap);

  // The assembly's midpoint must be the canvas midpoint.
  assert.equal(pillLeft + total / 2, 600);
});

test('the pill shifts left by exactly half the bubble assembly', () => {
  const without = pairLayout('compact', false, 1200).pillLeft;
  const withB = pairLayout('compact', true, 1200).pillLeft;
  assert.equal(without - withB, (BUBBLE.gap + BUBBLE.compact) / 2);
});

test('the bubble grows at peek', () => {
  assert.equal(pairLayout('peek', true, 1200).bubbleSize, BUBBLE.peek);
});

test('unknown presence falls back to compact rather than throwing', () => {
  const r = pairLayout('nonsense', true, 1200);
  assert.ok(Number.isFinite(r.pillLeft));
  assert.ok(Number.isFinite(r.bubbleLeft));
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
node --test internal/ui/assets/js/geometry.test.js
```

Expected: FAIL — `pairLayout` is not exported.

- [ ] **Step 3: Write `pairLayout`**

```js
// append to internal/ui/assets/js/geometry.js

// Bubble dimensions in CSS px. The bubble is the second visible activity —
// iPhone's detached circle beside the pill.
export const BUBBLE = { compact: 32, peek: 44, gap: 8 };

// pairLayout centers the PILL+BUBBLE ASSEMBLY, not the pill.
//
// This is the whole point: if the pill stayed plain-centered, every activity
// that started would visibly shove the island sideways as the bubble appeared,
// which reads as a bug even though it is deliberate. Instead the pill slides
// left by exactly half the bubble's total width, so the pair's midpoint stays
// on the canvas midpoint and only the bubble appears to arrive.
//
// Pure: takes the canvas width, returns positions. No DOM.
export function pairLayout(presence, hasBubble, canvasWidth) {
  const size = PRESENCE_SIZES[presence] || PRESENCE_SIZES.compact;
  if (!hasBubble) {
    return { pillLeft: (canvasWidth - size.w) / 2, bubbleLeft: null, bubbleSize: 0 };
  }
  const bubbleSize = presence === 'peek' ? BUBBLE.peek : BUBBLE.compact;
  const total = size.w + BUBBLE.gap + bubbleSize;
  const pillLeft = (canvasWidth - total) / 2;
  return { pillLeft, bubbleLeft: pillLeft + size.w + BUBBLE.gap, bubbleSize };
}
```

Add `import { PRESENCE_SIZES } from './state.js';` if it is not already imported (it is — `unionIslandRect` uses it).

- [ ] **Step 4: Run tests to verify they pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
node --test internal/ui/assets/js/geometry.test.js
```

Expected: PASS (9 tests — 4 existing plus 5 new)

- [ ] **Step 5: Commit**

```bash
git add internal/ui/assets/js/geometry.js internal/ui/assets/js/geometry.test.js
git commit -m "feat(ui): pairLayout centers the pill+bubble assembly

The pill shifts left by half the bubble's width so the pair's midpoint stays on
the canvas midpoint. Without this, every activity that starts visibly shoves the
island sideways. Pure function, tested.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 9: The bubble — markup, styles, motion, region, interaction

Everything visible. The bubble renders the second activity, animates in and out, is included in the window region, and promotes to the pill on click.

**Files:**
- Modify: `internal/ui/assets/index.html`
- Modify: `internal/ui/assets/css/island.css`
- Modify: `internal/ui/assets/js/main.js`
- Modify: `internal/ui/assets/js/activities.js` (timer + meeting render slots)

**Interfaces:**
- Consumes: `resolve` → `{bubbleId}` (Task 7); `pairLayout` (Task 8); `renderProvided` (Task 6); `publishRegionRects`, `collectOtherSurfaceRects` (SP5)
- Produces: `window.promoteBubble()`, `window.dismissActivity(id)`

- [ ] **Step 1: Add the markup**

In `index.html`, immediately after the `#island` element:

```html
  <div id="bubble" class="glass" onclick="window.promoteBubble()" title="Switch to this activity"></div>
```

- [ ] **Step 2: Add the styles**

In `island.css`:

```css
/* The detached second activity. Positioned from JS via pairLayout so the
   pill+bubble pair stays centered; CSS owns only appearance and transition. */
#bubble{
  position:fixed; top:10px; left:0;
  width:32px; height:32px; border-radius:50%;
  display:none; place-items:center; overflow:hidden;
  cursor:pointer; z-index:6;
  transform:scale(0); opacity:0;
  will-change:transform,opacity,left,width,height;
}
#bubble.shown{display:grid; transform:scale(1); opacity:1}
/* Arrive with a slight overshoot so it reads as physical; leave without one,
   because a bouncing departure reads as unstable. Same asymmetry as the pill. */
#bubble.entering{transition:transform 380ms cubic-bezier(.22,1.16,.36,1), opacity 200ms linear}
#bubble.leaving {transition:transform 260ms cubic-bezier(.36,0,.24,1), opacity 180ms linear}
#bubble img{width:100%;height:100%;object-fit:cover}
@media (prefers-reduced-motion:reduce){ #bubble{transition:none!important} }
```

- [ ] **Step 3: Add the timer and meeting render slots**

In `activities.js`, add a `kindRenderers` map keyed by `Activity.Kind` (the provider path uses `kind`, not `id`, so one renderer serves every timer):

```js
const kindRenderers = {
  timer: {
    bubble:  (d) => el('span', 'ring', ringSVG(d.remaining, d.total)),
    leading: (d) => el('span', 'ring', ringSVG(d.remaining, d.total)),
    compact: (d) => el('div', null,
      `<span class="ttl">${mmss(d.remaining)}</span>` +
      `<span class="sub">${esc(d.label || 'Timer')}</span>`),
    expanded: (d) => el('div', null,
      `<span class="ttl">${esc(d.label || 'Timer')}</span>` +
      `<span class="sub">${mmss(d.remaining)} remaining</span>`),
  },
  meeting: {
    bubble:  () => el('span', null, icon('calendar')),
    leading: () => el('span', null, icon('calendar')),
    compact: (d) => el('div', null,
      `<span class="ttl">${esc(d.title || 'Meeting')}</span>` +
      `<span class="sub">in ${d.minutes|0}m</span>`),
    expanded: (d) => {
      const n = el('div', null,
        `<span class="ttl">${esc(d.title || 'Meeting')}</span>` +
        `<span class="sub">starts in ${d.minutes|0}m</span>`);
      if(d.joinURL){
        const b = el('button', 'btn primary', 'Join');
        b.onclick = (ev) => { ev.stopPropagation(); window.openExternal &&
                              window.openExternal(d.joinURL) };
        n.appendChild(b);
      }
      return n;
    },
  },
};

function mmss(sec){
  const s = Math.max(0, sec|0);
  return String((s/60)|0).padStart(2,'0') + ':' + String(s%60).padStart(2,'0');
}

// A countdown ring drawn with stroke-dashoffset. r=9 gives circumference ~56.5.
function ringSVG(remaining, total){
  const frac = total > 0 ? Math.max(0, Math.min(1, remaining/total)) : 0;
  const c = 2 * Math.PI * 9;
  return `<svg class="ico" viewBox="0 0 24 24">` +
    `<circle cx="12" cy="12" r="9" stroke="rgba(255,255,255,.18)"/>` +
    `<circle cx="12" cy="12" r="9" stroke="currentColor" ` +
    `stroke-dasharray="${c.toFixed(1)}" stroke-dashoffset="${(c*(1-frac)).toFixed(1)}" ` +
    `transform="rotate(-90 12 12)"/></svg>`;
}
```

Export `kindRenderers` so `renderProvided` (Task 6) resolves against it.

- [ ] **Step 4: Render and position the bubble in `rerender()`**

In `main.js`, after the existing presence and content branches and **before** the region publish:

```js
  // ── bubble ────────────────────────────────────────────────────────────────
  if(r.bubbleId !== applied.bubbleId){
    if(r.bubbleId){
      bubble.replaceChildren(renderProvided(r.bubbleId,'bubble') ||
                             renderActivity(r.bubbleId,'leading') ||
                             document.createTextNode(''));
      bubble.classList.remove('leaving'); bubble.classList.add('entering','shown');
    } else {
      bubble.classList.remove('entering'); bubble.classList.add('leaving');
      bubble.classList.remove('shown');
    }
    applied.bubbleId = r.bubbleId;
  } else if(r.bubbleId){
    // Same activity, new data — refresh in place, no re-entry animation.
    bubble.replaceChildren(renderProvided(r.bubbleId,'bubble') ||
                           document.createTextNode(''));
  }

  // Position the pair. pairLayout centers the ASSEMBLY, not the pill.
  const lay = pairLayout(r.presence, !!r.bubbleId, canvasCSSWidth());
  island.style.left = lay.pillLeft + 'px';
  island.style.transform = 'none';   // pairLayout supersedes translateX(-50%)
  if(r.bubbleId){
    bubble.style.left = lay.bubbleLeft + 'px';
    bubble.style.width = lay.bubbleSize + 'px';
    bubble.style.height = lay.bubbleSize + 'px';
  }
```

`canvasCSSWidth()` reads the value the `getCanvasSize` binding already exposes (SP5 added it and nothing consumed it — this is its first real consumer). Cache it once on `uiReady` rather than calling the binding each frame.

**Important:** `#island`'s CSS currently centers with `left:50%; transform:translateX(-50%)`. Setting `left` from JS while that transform is still applied would double-offset it. Remove the transform in CSS and let `pairLayout` own horizontal position entirely, so there is exactly one source of truth for where the pill sits.

- [ ] **Step 4b: Keep a glyph visible at dormant (spec §3, "auto-shy changes meaning")**

Today `dormant` is an empty 168×32 capsule. With an activity running it must keep a minimal glyph — the countdown ring, album art, the calendar icon — or shy-by-default silently becomes hidden-by-default and the whole value of a Live Activity (the glance) is lost.

`updateCaps()` currently renders the leading cap for any content id. Confirm the cap is still *visible* at dormant size and does not get clipped by `#island`'s `overflow:hidden` at 32px height. In `island.css`, add:

```css
/* At dormant the island is 168x32 and text does not fit — but the leading
   glyph must survive, or shy-by-default becomes hidden-by-default. */
#island[data-presence="dormant"] .body{display:none}
#island[data-presence="dormant"] .cap.lead{margin:0 auto}
#island[data-presence="dormant"] .cap.trail{display:none}
```

Verify by eye in QA check 6: a running timer at dormant shows the ring and nothing else.

- [ ] **Step 5: Include the bubble in the window region**

The window's visible shape is the union of published rects — anything not covered is not part of the window. Extend `collectOtherSurfaceRects()` in `main.js` to include `#bubble` when it has the `shown` class, exactly as `#toast` is handled:

```js
  document.querySelectorAll('.panel.active, .card.shown, #dashboard.visible, #toast, #bubble.shown')
```

Because the bubble animates, its rect changes during the transition. Publish on **enter and settle** the same way the pill does: call `publishRegionRects()` immediately when the bubble becomes shown, and again from a `transitionend` handler on `#bubble`. Do not publish per frame.

- [ ] **Step 6: Wire promote and dismiss**

```js
// Clicking the bubble swaps it into the main pill. Implemented as a priority
// nudge rather than a separate "pinned" concept: resolve() stays the only thing
// that decides slots.
window.promoteBubble = () => {
  if(!applied.bubbleId) return;
  store.promoted = applied.bubbleId;
  rerender();
};

window.dismissActivity = (id) => {
  window.dismissIslandActivity && window.dismissIslandActivity(id);
  endActivity(id, syncAndRerender);
};
```

In `resolve()`, honor `store.promoted`: if it names a currently-live activity, move it to the front of the sorted list before slot assignment, then clear it when that activity ends. Add a `state.test.js` case asserting a promoted second activity becomes `contentId` and the previous first becomes `bubbleId`.

Add a Go binding `dismissIslandActivity(id string)` in `overlay.go` calling `island.DefaultRegistry.Dismiss(id)` (the registry instance is created in Task 10; use a package-level var in `internal/ui` set by `main.go` to avoid an import cycle).

- [ ] **Step 7: Verify**

```bash
export PATH="$PATH:/c/w64devkit/bin"
node --test internal/ui/assets/js/state.test.js internal/ui/assets/js/geometry.test.js internal/ui/assets/js/activities.test.js internal/ui/assets/js/motion.test.js internal/ui/assets/js/surfaces.test.js
go test ./internal/... && go build ./... && go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
```

Also confirm `resolve()` is still the only geometry authority:

```bash
grep -rn "morphTo\|style.width\|style.height\|borderRadius" internal/ui/assets/js/ | grep -v motion.js | grep -v "bubble.style"
```

Expected: no hits other than the bubble sizing in `rerender()`, which is horizontal layout rather than island presence — note it in your report.

- [ ] **Step 8: Commit**

```bash
git add internal/ui/assets/
git commit -m "feat(ui): the split island — detached bubble for the second activity

Bubble renders the second-ranked activity, enters with overshoot and leaves
without, is included in the window region on enter and settle, and promotes to
the main pill on click. pairLayout owns horizontal position for both, replacing
the pill's translateX centering so there is one source of truth.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 10: Wire it up and QA

**Files:**
- Modify: `cmd/app/main.go`
- Modify: `internal/ui/overlay.go` (the `dismissIslandActivity` binding)
- Create: `docs/superpowers/plans/2026-08-01-sp6-qa-checklist.md`

**Interfaces:**
- Consumes: everything from Tasks 1–9

- [ ] **Step 1: Construct and start the registry**

In `cmd/app/main.go`, after the UI is initialized and before `ui.StartOverlay`:

```go
	islandReg := island.NewRegistry(island.SystemClock{}, ui.PublishActivities)
	ui.SetIslandRegistry(islandReg) // lets the dismiss binding reach it

	meetings := island.NewMeetingProvider(island.SystemClock{}, func(ctx context.Context) (*island.NextMeeting, error) {
		return nextMeetingFromCalendar(ctx, cfg)
	})
	runner := island.NewRunner(islandReg, island.DefaultTimers, meetings)
	runner.Start(rootCtx)
```

`nextMeetingFromCalendar` is a thin adapter over the existing Google Calendar integration. **Read `internal/tools/google_calendar.go` (or whichever file defines `GoogleCalendarListTool`) before writing it** — reuse that tool's existing fetch path rather than writing a second Calendar client. Its contract:

```go
// nextMeetingFromCalendar returns the soonest event that has not yet started,
// or (nil, nil) when there is none. It must return a non-nil error on API
// failure — returning (nil, nil) there would look identical to "no meetings"
// and the runner's backoff would never engage.
func nextMeetingFromCalendar(ctx context.Context, cfg *config.Config) (*island.NextMeeting, error)
```

Map the event's summary to `Title`, its start time to `StartsAt`, and its hangout/conference link (if any) to `JoinURL`. If the user has not linked Google, return `(nil, nil)` — no meetings is the correct answer, not an error, and erroring would spin the backoff loop forever.

- [ ] **Step 2: Add the dismiss plumbing**

```go
// internal/ui/activity.go
var islandRegistry interface{ Dismiss(string) }

func SetIslandRegistry(r interface{ Dismiss(string) }) { islandRegistry = r }
```

Bind it in `StartOverlay`:

```go
	w.Bind("dismissIslandActivity", func(id string) {
		if islandRegistry != nil {
			islandRegistry.Dismiss(id)
		}
	})
```

Using a narrow interface rather than importing the concrete type keeps `internal/ui` from depending on the registry's construction.

- [ ] **Step 3: Full verification**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/... -race
node --test internal/ui/assets/js/state.test.js internal/ui/assets/js/geometry.test.js internal/ui/assets/js/activities.test.js internal/ui/assets/js/motion.test.js internal/ui/assets/js/surfaces.test.js
go build ./... && go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
go list -f '{{join .Imports "
"}}' ./internal/island/ | grep -E "\.|/" | grep -v "^[a-z/]*$" && echo "DEPENDENCY VIOLATION" || echo "island is stdlib-only - correct"
```

- [ ] **Step 4: Write the QA checklist**

Create `docs/superpowers/plans/2026-08-01-sp6-qa-checklist.md` with these checks, each with a Result column:

| # | Check | Pass criteria |
|---|---|---|
| 1 | Start a 2-minute timer | Island shows a countdown with a draining ring; it ticks without twitching or re-animating each second |
| 2 | Start a timer, then play music | Island splits: one owns the pill, the other becomes a bubble beside it |
| 3 | Watch the split appear | The **pair** stays centered — the island must not jump sideways when the bubble arrives |
| 4 | Click the bubble | The two swap; the promoted activity takes the pill |
| 5 | Let the timer reach zero | The island wakes to peek immediately, with no 250ms delay |
| 6 | Leave a timer running and go idle 10s | Island goes dormant but keeps the ring glyph — the glance survives the shrink |
| 7 | Dismiss a running timer | It disappears from the island; the timer still fires on time |
| 8 | Trigger an approval while music plays | Approval takes the pill, music demotes to the bubble; approval still fails closed on dismiss |
| 9 | Click beside the bubble | The click reaches the desktop — the bubble's region must not be oversized |
| 10 | Meeting within 60m | Countdown appears; wakes at T-5m and T-1m, and only once each |
| 11 | Kill network, wait 2 min | Meeting provider retries with backoff; no crash, no stuck activity, log shows the retry |
| 12 | 8+ concurrent timers | Registry caps at 8; a log line records the drop; the UI stays responsive |
| 13 | `voice-agent.log` | No `unhandled event`, no `unknown activity`, no JS errors |

- [ ] **Step 5: Commit**

```bash
git add cmd/app/main.go internal/ui/ docs/superpowers/plans/2026-08-01-sp6-qa-checklist.md
git commit -m "feat(island): wire the registry, providers and dismiss binding

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Deferred to SP7–SP9

SMTC system-wide now-playing, mic/camera in-use + global mute, call detection, recording indicator (SP7 — one WinRT spike gates all four). Smart clipboard actions, AI email triage, live meeting assistant (SP8 — the meeting assistant needs an explicit consent decision, since it means continuously recording meeting audio). Sports, stocks, package tracking, file-transfer progress (SP9 — all need user-supplied API keys and must degrade gracefully offline).

Carried-forward issues unchanged by this plan: 175%/200% DPI break the fixed window; webview_go's own `WM_DPICHANGED` handler resizes the window independently of `canvas.Attach()`.
