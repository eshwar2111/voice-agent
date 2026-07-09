package ambient

import "time"

// dedupWindow: don't repeat the same DedupKey within this span.
const dedupWindow = 6 * time.Hour

// Policy is the pure "should this be shown now?" gate.
type Policy struct {
	MinGap    time.Duration
	seen      map[string]time.Time
	lastShown time.Time
}

func NewPolicy(minGap time.Duration) *Policy {
	return &Policy{MinGap: minGap, seen: make(map[string]time.Time)}
}

// Allow reports whether s may be shown at time now given the busy state.
func (p *Policy) Allow(s Suggestion, now time.Time, busy bool) bool {
	if busy {
		return false
	}
	if !p.lastShown.IsZero() && now.Sub(p.lastShown) < p.MinGap {
		return false
	}
	if t, ok := p.seen[s.DedupKey]; ok && now.Sub(t) < dedupWindow {
		return false
	}
	return true
}

// Record marks s as shown at now (call only when actually delivered).
func (p *Policy) Record(s Suggestion, now time.Time) {
	p.seen[s.DedupKey] = now
	p.lastShown = now
}
