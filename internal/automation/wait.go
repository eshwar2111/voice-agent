package automation

import (
	"context"
	"time"
)

// Sleep pauses execution for the given number of seconds.
// Used in automation sequences to wait for UI elements to appear.
func Sleep(seconds float64) {
	time.Sleep(time.Duration(seconds * float64(time.Second)))
}

// SleepCtx is a context-aware sleep that can be cancelled early.
// Returns ctx.Err() if cancelled before the sleep completes.
func SleepCtx(ctx context.Context, seconds float64) error {
	timer := time.NewTimer(time.Duration(seconds * float64(time.Second)))
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
