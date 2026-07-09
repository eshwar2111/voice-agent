//go:build !whisper

package wakeword

import "context"

// StartWakeWordLoop is a no-op in the default (no-voice) build.
func StartWakeWordLoop(ctx context.Context, accessKey string, onDetect func(), isBusy func() bool) error {
	<-ctx.Done()
	return nil
}
