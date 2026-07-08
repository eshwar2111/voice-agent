package wakeword

import "context"

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
func runWakeLoop(ctx context.Context, src FrameSource, det Detector, onDetect func()) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		frame, err := src.Read()
		if err != nil {
			continue // transient read error; keep listening
		}
		idx, err := det.Process(frame)
		if err != nil {
			continue
		}
		if idx >= 0 {
			_ = src.Stop()
			onDetect() // blocks until the command completes
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if err := src.Start(); err != nil {
				return err
			}
		}
	}
}
