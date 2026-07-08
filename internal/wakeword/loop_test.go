package wakeword

import (
	"context"
	"testing"
	"time"
)

type fakeSource struct {
	started, stopped int
	startedCh        chan struct{}
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
func (f *fakeSource) Stop() error { f.stopped++; return nil }

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

	err := runWakeLoop(ctx, src, det, onDetect)
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
	if err := runWakeLoop(ctx, src, det, func() {}); err != nil {
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
		errCh <- runWakeLoop(ctx, src, det, onDetect)
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
