package wakeword

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	started, stopped int
	startedCh        chan struct{}
	stoppedCh        chan struct{}
}

func (f *fakeSource) Read() ([]int16, error) { return []int16{0}, nil }
func (f *fakeSource) Start() error {
	f.started++
	if f.startedCh != nil {
		select {
		case f.startedCh <- struct{}{}:
		default:
		}
	}
	return nil
}
func (f *fakeSource) Stop() error {
	f.stopped++
	if f.stoppedCh != nil {
		select {
		case f.stoppedCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// detector that fires the keyword once, then never again.
type onceDetector struct{ fired bool }

func (d *onceDetector) Process([]int16) (int, error) {
	if !d.fired {
		d.fired = true
		return 0, nil // keyword index 0 = detected
	}
	return -1, nil
}

func TestRunWakeLoopHandsOffMicAroundOnDetect(t *testing.T) {
	src := &fakeSource{}
	det := &onceDetector{}
	ctx, cancel := context.WithCancel(context.Background())

	onDetect := func() {
		// When onDetect runs, the mic must already be released.
		if src.stopped != 1 {
			t.Errorf("recorder not stopped before onDetect (stopped=%d)", src.stopped)
		}
		cancel() // end the loop after one detection
	}

	err := runWakeLoop(ctx, src, det, onDetect, func() bool { return false })
	if err != nil {
		t.Fatalf("runWakeLoop returned error: %v", err)
	}
	if src.stopped < 1 {
		t.Errorf("expected Stop() around onDetect")
	}
}

func TestRunWakeLoopExitsOnCancel(t *testing.T) {
	src := &fakeSource{}
	det := &onceDetector{fired: true} // never detects
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := runWakeLoop(ctx, src, det, func() {}, func() bool { return false }); err != nil {
		t.Fatalf("expected clean exit on cancel, got %v", err)
	}
}

// TestRunWakeLoopRestartsAfterOnDetect proves the mic baton is fully returned:
// after onDetect finishes (without cancelling), the loop must call src.Start()
// again before continuing to read frames.
func TestRunWakeLoopRestartsAfterOnDetect(t *testing.T) {
	src := &fakeSource{startedCh: make(chan struct{}, 1)}
	det := &onceDetector{}
	ctx, cancel := context.WithCancel(context.Background())

	onDetect := func() {
		// At the moment onDetect runs, the source must be stopped and not yet restarted.
		if src.stopped != 1 {
			t.Errorf("expected src.stopped==1 when onDetect runs, got %d", src.stopped)
		}
		if src.started != 0 {
			t.Errorf("expected src.started==0 when onDetect runs, got %d", src.started)
		}
		// Deliberately does NOT cancel here — we want to observe the restart.
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWakeLoop(ctx, src, det, onDetect, func() bool { return false })
	}()

	select {
	case <-src.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Start never called")
	}

	if src.started < 1 {
		t.Errorf("expected src.Start() to be called after onDetect returned, got started=%d", src.started)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runWakeLoop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWakeLoop did not return after cancel")
	}
}

// busyFlag is a small deterministic, goroutine-safe toggle used to simulate the
// engine's IsBusy() gate from a separate goroutine.
type busyFlag struct {
	mu sync.Mutex
	v  bool
}

func (b *busyFlag) set(v bool) {
	b.mu.Lock()
	b.v = v
	b.mu.Unlock()
}

func (b *busyFlag) get() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.v
}

// countingDetector never fires a wake word; it just counts how many frames were
// actually processed, so the test can prove no processing happens while busy.
type countingDetector struct{ calls int32 }

func (d *countingDetector) Process([]int16) (int, error) {
	atomic.AddInt32(&d.calls, 1)
	return -1, nil
}

// TestRunWakeLoopPausesWhenBusy proves the wake loop releases (Stop()s) its own
// recorder whenever the engine reports it is busy with another command capture
// (pill or wake-triggered), stops processing frames while busy, and resumes
// (Start()s again) once the engine reports it is free. The recorder is assumed
// already running on entry (as StartWakeWordLoop does via rec.Start() before
// handing off to runWakeLoop), so no Start() is expected until after the first
// busy/resume cycle.
func TestRunWakeLoopPausesWhenBusy(t *testing.T) {
	src := &fakeSource{
		startedCh: make(chan struct{}, 4),
		stoppedCh: make(chan struct{}, 4),
	}
	det := &countingDetector{}
	busy := &busyFlag{v: false}
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWakeLoop(ctx, src, det, func() {}, busy.get)
	}()

	// Let the loop process at least one frame while not busy, proving it is
	// actively reading before we introduce contention.
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&det.calls) == 0 {
		select {
		case <-deadline:
			t.Fatal("loop never processed a frame before going busy")
		default:
		}
	}

	// Flip busy on: the loop must release the mic.
	busy.set(true)
	select {
	case <-src.stoppedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop never called after going busy")
	}

	// Snapshot how many frames were processed at the moment we stopped, then
	// confirm no further frames are processed while busy remains true. The
	// loop's own 50ms busy-poll sleep gives this a deterministic window: if a
	// resume happened it would show up as a new Start() before we re-check.
	callsAtStop := atomic.LoadInt32(&det.calls)
	time.Sleep(120 * time.Millisecond) // several busy-poll iterations
	if got := atomic.LoadInt32(&det.calls); got != callsAtStop {
		t.Errorf("expected no frame processing while busy, calls went from %d to %d", callsAtStop, got)
	}
	if src.started != 0 {
		t.Errorf("expected recorder to remain stopped (no Start calls) while busy, started=%d", src.started)
	}

	// Flip busy off: the loop must resume (Start() again).
	busy.set(false)
	select {
	case <-src.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Start never called again after busy cleared")
	}
	if src.started < 1 {
		t.Errorf("expected recorder to restart after busy cleared, started=%d", src.started)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runWakeLoop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWakeLoop did not return after cancel")
	}
}
