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
