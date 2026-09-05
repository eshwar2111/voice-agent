package audio

import (
	"encoding/binary"
	"errors"
	"log"
	"math"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/malgo"
)

// frameRing buffers int16 samples pushed from the capture callback and hands out
// fixed-size frames to a blocking Read. Split out from the device so its FIFO /
// close behavior is unit-testable without a microphone.
type frameRing struct {
	frameLen int
	mu       sync.Mutex
	cond     *sync.Cond
	buf      []int16
	closed   bool
}

func newFrameRing(frameLen int) *frameRing {
	r := &frameRing{frameLen: frameLen}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *frameRing) push(s []int16) {
	r.mu.Lock()
	r.buf = append(r.buf, s...)
	r.mu.Unlock()
	r.cond.Broadcast()
}

func (r *frameRing) read() ([]int16, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.buf) < r.frameLen && !r.closed {
		r.cond.Wait()
	}
	if r.closed {
		return nil, errors.New("mic source closed")
	}
	out := make([]int16, r.frameLen)
	copy(out, r.buf[:r.frameLen])
	r.buf = r.buf[r.frameLen:]
	return out, nil
}

func (r *frameRing) close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cond.Broadcast()
}

// MicSource is a malgo-backed wakeword.FrameSource: it captures 16 kHz mono audio
// and yields fixed-length int16 frames, replacing the Picovoice recorder so all
// capture runs on one runtime.
type MicSource struct {
	frameLen int
	ring     *frameRing
	ctx      *malgo.AllocatedContext
	device   *malgo.Device
	frames   atomic.Int64 // debug: callback count since Start
}

func NewMicSource(frameLen int) *MicSource {
	return &MicSource{frameLen: frameLen, ring: newFrameRing(frameLen)}
}

func (m *MicSource) Start() error {
	// A fresh ring on every Start: Stop() closes the previous ring permanently
	// (so a blocked Read unblocks), so restarting after a busy period MUST hand
	// out a new, open ring or Read would return "closed" forever.
	m.ring = newFrameRing(m.frameLen)
	m.frames.Store(0)

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return err
	}
	m.ctx = ctx

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatF32
	cfg.Capture.Channels = channels
	cfg.SampleRate = sampleRate
	cfg.Alsa.NoMMap = 1

	onRecv := func(_, in []byte, _ uint32) {
		n := len(in) / 4
		s := make([]int16, n)
		var peak float32
		for i := 0; i < n; i++ {
			f := math.Float32frombits(binary.LittleEndian.Uint32(in[i*4:]))
			if f > 1 {
				f = 1
			} else if f < -1 {
				f = -1
			}
			if a := f; a < 0 {
				a = -a
			} else if a > peak {
				peak = a
			}
			s[i] = int16(f * 32767)
		}
		// Log the first callback after each Start (confirms the capture device is
		// actually delivering audio), then stay quiet.
		if m.frames.Add(1) == 1 {
			log.Printf("[micsource] capturing started: %d samples/frame, peak=%.3f", n, peak)
		}
		m.ring.push(s)
	}

	dev, err := malgo.InitDevice(ctx.Context, cfg, malgo.DeviceCallbacks{Data: onRecv})
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		m.ctx = nil
		return err
	}
	m.device = dev
	return dev.Start()
}

func (m *MicSource) Stop() error {
	if m.device != nil {
		_ = m.device.Stop()
		m.device.Uninit()
		m.device = nil
	}
	if m.ctx != nil {
		_ = m.ctx.Uninit()
		m.ctx.Free()
		m.ctx = nil
	}
	m.ring.close()
	return nil
}

func (m *MicSource) Read() ([]int16, error) { return m.ring.read() }
