package ambient

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recDeliverer struct {
	mu    sync.Mutex
	last  string
	count int64
}

func (r *recDeliverer) ShowSuggestion(id string, s Suggestion) {
	r.mu.Lock()
	r.last = id + ":" + s.Title
	r.mu.Unlock()
	atomic.AddInt64(&r.count, 1)
}
func (r *recDeliverer) lastShown() string { r.mu.Lock(); defer r.mu.Unlock(); return r.last }
func (r *recDeliverer) deliveryCount() int64 { return atomic.LoadInt64(&r.count) }

func newTestEngine(ui Deliverer, busy bool) *Engine {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	return &Engine{
		Policy:  NewPolicy(time.Minute),
		UI:      ui,
		Busy:    func() bool { return busy },
		Enabled: func() bool { return true },
		Now:     func() time.Time { return t0 },
	}
}

// newTestEngineWithClock is like newTestEngine but exposes a mutable clock so
// tests can advance time past the policy's MinGap to allow subsequent deliveries.
func newTestEngineWithClock(ui Deliverer, busy bool) (*Engine, *mutableClock) {
	mc := &mutableClock{t: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	e := &Engine{
		Policy:  NewPolicy(time.Minute),
		UI:      ui,
		Busy:    func() bool { return busy },
		Enabled: func() bool { return true },
		Now:     mc.now,
	}
	return e, mc
}

type mutableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *mutableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestEngineDeliversThenSuppressesDuplicate(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, false)
	s := Suggestion{Title: "Unzip?", DedupKey: "zip1"}
	e.consider(s)
	if ui.lastShown() == "" {
		t.Fatal("first suggestion should be delivered")
	}
	// while one is active, a second is dropped (one-at-a-time)
	before := ui.lastShown()
	e.consider(Suggestion{Title: "Other", DedupKey: "x"})
	if ui.lastShown() != before {
		t.Fatal("second suggestion must be dropped while one is active")
	}
}

func TestEngineSuppressedWhenBusy(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, true) // busy
	e.consider(Suggestion{Title: "x", DedupKey: "k"})
	if ui.lastShown() != "" {
		t.Fatal("nothing should be delivered while busy")
	}
}

func TestEngineAcceptRunsAction(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, false)
	done := make(chan struct{})
	e.consider(Suggestion{Title: "Do", DedupKey: "k", Run: func(context.Context) error { close(done); return nil }})
	// the delivered id is "sg1"
	e.Accept("sg1")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Accept should run the action")
	}
}

// TestEngineConcurrentConsiderDeliversExactlyOnce stresses the active==nil
// TOCTOU window in consider() by hammering it from many goroutines with the
// same dedup key concurrently. Only one should win.
func TestEngineConcurrentConsiderDeliversExactlyOnce(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, false)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			e.consider(Suggestion{Title: "x", DedupKey: "same"})
		}()
	}
	wg.Wait()

	if got := ui.deliveryCount(); got != 1 {
		t.Fatalf("expected exactly 1 delivery under concurrent consider, got %d", got)
	}
}

// TestEngineConcurrentAcceptRunsActionOnce ensures Accept's match-then-clear
// is atomic under concurrent callers, so the action never double-runs.
func TestEngineConcurrentAcceptRunsActionOnce(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, false)

	var runs int64
	var runWG sync.WaitGroup
	runWG.Add(1)
	e.consider(Suggestion{Title: "Do", DedupKey: "k", Run: func(context.Context) error {
		atomic.AddInt64(&runs, 1)
		runWG.Done()
		return nil
	}})

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			e.Accept("sg1")
		}()
	}
	wg.Wait()

	select {
	case <-waitGroupDone(&runWG):
	case <-time.After(time.Second):
		t.Fatal("action should have run at least once")
	}

	if got := atomic.LoadInt64(&runs); got != 1 {
		t.Fatalf("expected action to run exactly once, got %d", got)
	}
}

// waitGroupDone adapts a sync.WaitGroup to a channel so it can be used in a select.
func waitGroupDone(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch
}

// TestEngineDismiss covers clearing the active suggestion, recovery after
// dismissal (not a permanent latch), and dismiss-with-wrong-id being a no-op.
func TestEngineDismiss(t *testing.T) {
	ui := &recDeliverer{}
	e, clock := newTestEngineWithClock(ui, false)

	// First engine/deliverer: verify Dismiss with a mismatched id is a no-op —
	// the active suggestion must remain intact and a matching Accept still runs.
	runDone := make(chan struct{})
	e.consider(Suggestion{Title: "First", DedupKey: "k1", Run: func(context.Context) error {
		close(runDone)
		return nil
	}})
	if ui.deliveryCount() != 1 {
		t.Fatalf("expected 1 delivery, got %d", ui.deliveryCount())
	}

	e.Dismiss("wrong-id") // no-op; active suggestion must remain

	e.Accept("sg1") // should still match and run since Dismiss("wrong-id") was a no-op
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Accept should still run after a mismatched Dismiss")
	}

	// Now deliver a second suggestion and dismiss it correctly, then verify
	// recovery: a new consider with a different dedup key, after advancing
	// the clock past MinGap, delivers.
	clock.advance(2 * time.Minute)
	e.consider(Suggestion{Title: "Second", DedupKey: "k2"})
	if ui.deliveryCount() != 2 {
		t.Fatalf("expected 2nd delivery, got %d", ui.deliveryCount())
	}
	e.Dismiss("sg2")
	clock.advance(2 * time.Minute)
	e.consider(Suggestion{Title: "Third", DedupKey: "k3"})
	if got := ui.deliveryCount(); got != 3 {
		t.Fatalf("expected 3rd delivery after Dismiss+recovery, got %d", got)
	}
}

// TestEngineRecoveryAfterAccept verifies that after Accept clears the active
// suggestion, a subsequent consider (past MinGap, different key) delivers again.
func TestEngineRecoveryAfterAccept(t *testing.T) {
	ui := &recDeliverer{}
	e, clock := newTestEngineWithClock(ui, false)

	done := make(chan struct{})
	e.consider(Suggestion{Title: "First", DedupKey: "k1", Run: func(context.Context) error {
		close(done)
		return nil
	}})
	if ui.deliveryCount() != 1 {
		t.Fatalf("expected 1 delivery, got %d", ui.deliveryCount())
	}

	e.Accept("sg1")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Accept should run the action")
	}

	clock.advance(2 * time.Minute)
	e.consider(Suggestion{Title: "Second", DedupKey: "k2"})
	if got := ui.deliveryCount(); got != 2 {
		t.Fatalf("expected 2nd delivery after Accept+recovery, got %d", got)
	}
}
