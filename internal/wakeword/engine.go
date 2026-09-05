package wakeword

import (
	"context"

	"github.com/yourname/voice-agent/internal/audio"
)

// KWSFrameLen is the capture chunk (samples @16 kHz) fed to the wake detector
// per Read. 1280 = 80 ms is openWakeWord's native processing unit (one chunk =
// one classifier prediction). Exported because main.go passes it to NewMicSource.
const KWSFrameLen = 1280

// WakeEngine is the tag-independent handle main.go holds. The whisper build backs
// it with sherpa KWS; the non-whisper build has no implementation (NewKWS errors).
type WakeEngine interface {
	// Hearer returns a barge-in keyword detector sharing this engine's model.
	Hearer() audio.KeywordHearer
	// StartLoop runs the idle wake loop on src until ctx is cancelled, calling
	// onDetect (which must BLOCK until the command completes) on a wake-word hit.
	StartLoop(ctx context.Context, src FrameSource, onDetect func(), isBusy func() bool) error
	// Close releases the underlying model.
	Close()
}
