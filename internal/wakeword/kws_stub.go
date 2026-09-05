//go:build !whisper

package wakeword

import "errors"

// NewKWS is unavailable without the whisper (voice) build tag.
func NewKWS(modelDir, wakeWord string) (WakeEngine, error) {
	return nil, errors.New("wake word requires the whisper build")
}
