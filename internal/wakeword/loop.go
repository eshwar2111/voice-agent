package wakeword

import (
	"context"
	"time"
)

// FrameSource yields audio frames and controls mic capture.
type FrameSource interface {
	Read() ([]int16, error)
	Start() error
	Stop() error
}

// Detector processes a frame and returns a keyword index >= 0 on a wake-word hit.
type Detector interface {
	Process(frame []int16) (int, error)
}

// runWakeLoop reads frames until ctx is cancelled. On a wake-word hit it Stops the source
// (releasing the mic), runs onDetect (which must BLOCK until the command finishes), then
// Starts the source again. This is the microphone baton.
//
// If isBusy is non-nil and reports true (a command — pill or wake-initiated — is currently
// capturing via another recorder), the loop stops its own recorder and waits, so only one
// recorder ever holds the mic at a time.
func runWakeLoop(ctx context.Context, src FrameSource, det Detector, onDetect func(), isBusy func() bool) error {
	// Callers (e.g. StartWakeWordLoop) Start the source before handing it to this loop,
	// so the recorder is already running on entry.
	running := true

	stopIfRunning := func() {
		if running {
			_ = src.Stop()
			running = false
		}
	}
	startIfStopped := func() error {
		if !running {
			if err := src.Start(); err != nil {
				return err
			}
			running = true
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			stopIfRunning()
			return nil
		default:
		}

		if isBusy != nil && isBusy() {
			stopIfRunning()
			time.Sleep(50 * time.Millisecond)
			continue
		}

		if err := startIfStopped(); err != nil {
			return err
		}

		frame, err := src.Read()
		if err != nil {
			time.Sleep(10 * time.Millisecond) // avoid busy-spin on persistent mic failure
			continue // transient read error; keep listening
		}
		idx, err := det.Process(frame)
		if err != nil {
			time.Sleep(10 * time.Millisecond) // avoid busy-spin on persistent processing failure
			continue
		}
		if idx >= 0 {
			stopIfRunning()
			onDetect() // blocks until the command completes
		}
	}
}
