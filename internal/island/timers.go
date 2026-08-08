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
