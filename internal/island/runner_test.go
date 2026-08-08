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

// nextBackoff must double from base on each consecutive failure and cap at
// maxBackoff, so a permanently-down provider settles into a slow, bounded
// retry rate instead of retrying (and logging) forever at the base interval.
func TestNextBackoffEscalatesAndCaps(t *testing.T) {
	base := time.Second
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, time.Second},
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
	}
	for _, c := range cases {
		if got := nextBackoff(base, c.failures); got != c.want {
			t.Errorf("nextBackoff(%s, %d) = %s, want %s", base, c.failures, got, c.want)
		}
	}
	if got := nextBackoff(time.Second, 20); got != maxBackoff {
		t.Errorf("nextBackoff(%s, 20) = %s, want cap %s", time.Second, got, maxBackoff)
	}
}

// stubFlaky fails a fixed number of times, then returns a single successful
// (non-blocking) run, then fails again — letting a test observe escalation,
// the reset on success, and re-escalation from base afterward, without ever
// sleeping through a real backoff.
type stubFlaky struct {
	name      string
	mu        sync.Mutex
	runs      int
	failUntil int
}

func (s *stubFlaky) Name() string { return s.name }

func (s *stubFlaky) Run(ctx context.Context, emit func(Activity), end func(string)) error {
	s.mu.Lock()
	s.runs++
	n := s.runs
	s.mu.Unlock()
	if n == s.failUntil+1 {
		return nil // one clean, non-blocking success: must reset the ladder
	}
	return errors.New("still down")
}

// A provider that fails repeatedly must see its retry delay escalate; a
// successful run must reset the delay to base so a provider that recovers
// does not stay slow; and a subsequent failure must re-escalate from base,
// not continue where the old ladder left off. Asserted on the recorded delay
// sequence via afterAttempt rather than by sleeping through real backoffs.
func TestRunnerBackoffEscalatesThenResetsOnSuccess(t *testing.T) {
	r, _, _ := newTestRegistry()
	p := &stubFlaky{name: "flaky", failUntil: 3}

	rn := NewRunner(r, p)
	rn.backoff = time.Millisecond

	var mu sync.Mutex
	var delays []time.Duration
	rn.afterAttempt = func(name string, failures int, delay time.Duration) {
		mu.Lock()
		delays = append(delays, delay)
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rn.Start(ctx)

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delays) >= 5
	})
	cancel()

	mu.Lock()
	got := append([]time.Duration(nil), delays[:5]...)
	mu.Unlock()

	want := []time.Duration{
		time.Millisecond,     // failure 1
		2 * time.Millisecond, // failure 2
		4 * time.Millisecond, // failure 3
		time.Millisecond,     // success: reset to base
		time.Millisecond,     // failure 1 again post-reset, not a continued escalation
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("delay[%d] = %s, want %s (full sequence: %v)", i, got[i], w, got)
		}
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
