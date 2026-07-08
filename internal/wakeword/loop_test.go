package wakeword

import (
	"context"
	"testing"
	"time"
)

type fakeSource struct{ started, stopped int }

func (f *fakeSource) Read() ([]int16, error) { return []int16{0}, nil }
func (f *fakeSource) Start() error           { f.started++; return nil }
func (f *fakeSource) Stop() error            { f.stopped++; return nil }

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

	order := []string{}
	onDetect := func() {
		// When onDetect runs, the mic must already be released.
		if src.stopped != 1 {
			t.Errorf("recorder not stopped before onDetect (stopped=%d)", src.stopped)
		}
		order = append(order, "detect")
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
