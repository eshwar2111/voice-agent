package audio

import "testing"

// feed runs a slice of rms values through the detector and returns the frame
// index at which it fired, or -1 if it never fired.
func feed(b *BargeDetector, rms []float64) int {
	for i, v := range rms {
		if b.Feed(v) {
			return i
		}
	}
	return -1
}

func rep(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestBargeIn_HeadphonesQuietFloor(t *testing.T) {
	// Near-silent floor (headphones: no echo). Normal speech (0.10) must
	// interrupt — the absolute floor gate carries it.
	b := NewBargeDetector()
	seq := append(rep(0.001, 6), rep(0.10, 8)...)
	if got := feed(b, seq); got < 0 {
		t.Fatalf("headphones: expected barge-in on speech, never fired")
	}
}

func TestBargeIn_SpeakersEchoNoFalseTrigger(t *testing.T) {
	// Loud steady echo floor (speakers): the agent's own voice at ~0.06 must
	// NEVER be mistaken for the user interrupting.
	b := NewBargeDetector()
	seq := append(rep(0.06, 6), rep(0.06, 40)...)
	if got := feed(b, seq); got >= 0 {
		t.Fatalf("speakers: false barge-in on the agent's own echo at frame %d", got)
	}
}

func TestBargeIn_SpeakersUserTalksOver(t *testing.T) {
	// Same echo floor, but now the user talks over it clearly louder (0.20).
	b := NewBargeDetector()
	seq := append(rep(0.06, 6), rep(0.20, 8)...)
	if got := feed(b, seq); got < 0 {
		t.Fatalf("speakers: user talking over the agent should interrupt, never fired")
	}
}

func TestBargeIn_TransientClickIgnored(t *testing.T) {
	// A single loud frame (a keyboard clack) must not fire — sustain guards it.
	b := NewBargeDetector()
	seq := append(rep(0.001, 6), 0.30)
	seq = append(seq, rep(0.001, 6)...)
	if got := feed(b, seq); got >= 0 {
		t.Fatalf("transient click should be ignored, fired at frame %d", got)
	}
}

func TestBargeIn_NeverFiresDuringCalibration(t *testing.T) {
	// Even loud input during the calibration window must not fire (that IS the
	// floor being measured).
	b := NewBargeDetector()
	for i := 0; i < b.calibN; i++ {
		if b.Feed(0.5) {
			t.Fatalf("fired during calibration at frame %d", i)
		}
	}
}

func TestBargeIn_Latches(t *testing.T) {
	b := NewBargeDetector()
	if feed(b, append(rep(0.001, 6), rep(0.10, 8)...)) < 0 {
		t.Fatal("expected fire")
	}
	if !b.Fired() || !b.Feed(0.0) {
		t.Fatal("detector must latch fired")
	}
}
