package wakeword

import (
	"testing"
)

// NewKWS must fail cleanly when the model dir is absent (both build tags): the
// caller degrades to "wake word off", never a crash.
func TestNewKWSMissingModelFailsCleanly(t *testing.T) {
	eng, err := NewKWS("does/not/exist", "hey jarvis")
	if err == nil {
		if eng != nil {
			eng.Close()
		}
		t.Fatal("expected an error when the KWS model dir is missing")
	}
	if eng != nil {
		t.Fatal("engine must be nil on error")
	}
}
