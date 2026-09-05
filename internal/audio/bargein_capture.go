package audio

import (
	"context"
	"encoding/binary"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

// KeywordHearer is an optional wake/stop-word detector fed the same PCM frames
// as the energy detector while the agent is speaking. Returning true means an
// explicit interrupt word (the wake word, or "stop") was heard and TTS should
// halt. It is the GUARANTEED interrupt path on speakers, where echo makes the
// energy detector deliberately conservative. nil disables it (open-mic only).
type KeywordHearer interface {
	// Hear consumes one frame of 16 kHz mono int16 PCM and reports a keyword hit.
	Hear(pcm16 []int16) bool
}

// rmsBytesToInt16 converts a miniaudio F32 byte buffer to int16 PCM and its RMS
// in one pass (the int16 frames feed an optional keyword detector; the RMS feeds
// the energy detector).
func rmsBytesToInt16(in []byte) ([]int16, float64) {
	n := len(in) / 4
	pcm := make([]int16, n)
	var sumSquares float64
	for i := range n {
		f := math.Float32frombits(binary.LittleEndian.Uint32(in[i*4:]))
		sumSquares += float64(f) * float64(f)
		if f > 1 {
			f = 1
		} else if f < -1 {
			f = -1
		}
		pcm[i] = int16(f * 32767)
	}
	rms := 0.0
	if n > 0 {
		rms = math.Sqrt(sumSquares / float64(n))
	}
	return pcm, rms
}

// WatchForBargeIn opens the microphone and watches for the user interrupting the
// agent while it speaks. It calls onBarge exactly once — on either an
// echo-adaptive energy trigger (talk-over) OR a keyword hit (kw, may be nil) —
// then returns. It also returns when ctx is cancelled (i.e. speech finished
// normally), having called onBarge zero times. Safe to call with a mic already
// contended: it fails quiet (logs nothing, just returns) so it can never wedge
// the speech path.
func WatchForBargeIn(ctx context.Context, kw KeywordHearer, onBarge func()) {
	mctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return
	}
	defer func() { _ = mctx.Uninit(); mctx.Free() }()

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatF32
	cfg.Capture.Channels = channels
	cfg.SampleRate = sampleRate
	cfg.Alsa.NoMMap = 1

	det := NewBargeDetector()
	var fired atomic.Bool
	trip := func() {
		if fired.CompareAndSwap(false, true) {
			onBarge()
		}
	}

	// Keyword frames must be a fixed length; buffer across variable callbacks.
	var kbuf []int16
	var kmu sync.Mutex
	const kwFrame = 512 // Porcupine's frame length

	onRecv := func(_, in []byte, _ uint32) {
		if fired.Load() {
			return
		}
		pcm, rms := rmsBytesToInt16(in)
		if det.Feed(rms) {
			trip()
			return
		}
		if kw != nil {
			kmu.Lock()
			kbuf = append(kbuf, pcm...)
			for len(kbuf) >= kwFrame {
				frame := kbuf[:kwFrame]
				kbuf = kbuf[kwFrame:]
				if kw.Hear(frame) {
					kmu.Unlock()
					trip()
					return
				}
			}
			kmu.Unlock()
		}
	}

	device, err := malgo.InitDevice(mctx.Context, cfg, malgo.DeviceCallbacks{Data: onRecv})
	if err != nil {
		return
	}
	defer device.Uninit()
	if err := device.Start(); err != nil {
		return
	}
	defer func() { _ = device.Stop() }()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if fired.Load() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
