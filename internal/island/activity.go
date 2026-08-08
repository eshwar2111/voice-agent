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
