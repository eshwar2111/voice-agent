package audio

// BargeDetector decides whether the user is talking OVER the agent's own TTS.
//
// The hard part of barge-in is echo: on laptop speakers the agent's synthesized
// voice bleeds back into the microphone, so a naive energy gate self-triggers on
// the very first syllable the agent speaks. This detector calibrates an
// echo/noise FLOOR from the first frames after speech starts (before the user
// could plausibly be interrupting), then flags an interruption only when the
// input sustains ABOVE that floor by a margin.
//
// The effect is exactly the "both modes" behavior:
//   - Headphones: the floor is near silence, so the absolute-floor gate
//     (absFloor) dominates and any normal speech interrupts naturally.
//   - Speakers: the floor sits at the echo level, so the margin gate dominates
//     and the user must clearly exceed the agent's own bleed to interrupt —
//     which is why the wake-word interrupt runs alongside as a guaranteed path.
//
// It is deliberately pure (RMS in, bool out) so the tuning is unit-testable
// without a live microphone.
type BargeDetector struct {
	calibN   int     // frames averaged to establish the echo floor
	margin   float64 // input must exceed floor*margin to count on speakers
	absFloor float64 // absolute minimum RMS to ever count (kills dead-silence noise)
	sustain  int     // consecutive over-threshold frames required to fire

	seen     int
	floorSum float64
	floor    float64
	over     int
	fired    bool
}

// NewBargeDetector returns a detector tuned for 16 kHz mono capture with the
// ~100 ms callback frames miniaudio delivers. Defaults were chosen to strongly
// favor NOT firing on the agent's own voice (a false barge-in — cutting the
// agent off mid-word for no reason — is far more irritating than a missed one,
// which the wake-word path still catches).
func NewBargeDetector() *BargeDetector {
	return &BargeDetector{
		calibN:   6,    // ~first 600 ms of speech = the echo floor
		margin:   1.8,  // must be ~2x the echo to count over speakers
		absFloor: 0.02, // below this is never speech, whatever the floor
		sustain:  4,    // ~400 ms of sustained voice, not a transient click
	}
}

// Feed reports whether a barge-in has been detected as of this frame's rms.
// Once it fires it latches true. Calibration consumes the first calibN frames
// and never fires during them.
func (b *BargeDetector) Feed(rms float64) bool {
	if b.fired {
		return true
	}
	if b.seen < b.calibN {
		b.floorSum += rms
		b.seen++
		if b.seen == b.calibN {
			b.floor = b.floorSum / float64(b.calibN)
		}
		return false
	}

	threshold := b.absFloor
	if t := b.floor * b.margin; t > threshold {
		threshold = t
	}

	if rms >= threshold {
		b.over++
		if b.over >= b.sustain {
			b.fired = true
			return true
		}
	} else {
		b.over = 0
	}
	return false
}

// Fired reports whether a barge-in has already been detected.
func (b *BargeDetector) Fired() bool { return b.fired }
