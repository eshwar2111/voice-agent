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

	lastPush time.Time
	pending  bool
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
