package wakeword

import (
	"context"

	"github.com/yourname/voice-agent/internal/audio"
)

// KWSFrameLen is the capture chunk (samples @16 kHz) fed to KWS per Read. The
// sherpa transducer accepts arbitrary chunk sizes; 480 = 30 ms feeds the decoder
// often (better recall + lower wake latency than 100 ms chunks) without spinning
// the loop on tiny buffers. Exported because main.go passes it to NewMicSource.
const KWSFrameLen = 480

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
