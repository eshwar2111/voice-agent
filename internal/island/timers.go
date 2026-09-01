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
	// total is the timer's original duration, captured once at Add. The ring
	// fraction is remaining/total, so total is kept as its own constant rather
	// than recomputed from endsAt-started: Resume shifts endsAt forward, and
	// deriving total from the moved endsAt would make the ring jump on resume.
	total time.Duration
	// paused freezes the countdown. While paused, remaining holds the frozen
	// time left and snapshot stops draining; Resume rebuilds endsAt from it.
	paused    bool
	remaining time.Duration
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
	now := t.clock.Now()
	t.items[id] = timerEntry{label: label, started: now, endsAt: endsAt, total: endsAt.Sub(now)}
}

func (t *Timers) Remove(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.items, id)
}

// Pause freezes a running timer: it records the time left at this instant and
// stops the countdown from draining (snapshot then reports that frozen value
// every tick). A no-op for an unknown or already-paused id.
func (t *Timers) Pause(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.items[id]
	if !ok || e.paused {
		return
	}
	rem := e.endsAt.Sub(t.clock.Now())
	if rem < 0 {
		rem = 0
	}
	e.paused = true
	e.remaining = rem
	t.items[id] = e
}

// Resume restarts a paused timer from its frozen remaining time by anchoring a
// fresh endsAt at now+remaining. started is left untouched: dismissal is keyed
// on ID+Started (see snapshot), so moving it would silently clear a dismissal,
// and total is stored independently so the ring fraction stays continuous
// across the pause. A no-op for an unknown or non-paused id.
func (t *Timers) Resume(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.items[id]
	if !ok || !e.paused {
		return
	}
	e.endsAt = t.clock.Now().Add(e.remaining)
	e.paused = false
	e.remaining = 0
	t.items[id] = e
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
		var remaining int
		if e.paused {
			remaining = int(e.remaining.Seconds()) // frozen — does not drain
		} else {
			remaining = int(e.endsAt.Sub(now).Seconds())
		}
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
				"total":     int(e.total.Seconds()),
				"paused":    e.paused,
			},
			// Started must be the timer's own start, NOT time.Now(): dismissal
			// is keyed on ID+Started, so a moving Started would clear the
			// dismissal on every tick.
			Started: e.started,
			Ends:    e.endsAt,
			// Only the moment it reaches zero is worth waking the island for.
			// A paused timer is holding, not firing, so it never wakes it.
			Significant: !e.paused && remaining == 0,
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
