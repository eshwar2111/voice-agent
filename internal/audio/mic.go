package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

// levelSink, when set, receives the RMS (0..~1) of every captured frame so the
// UI can draw a live listening waveform that reacts to the user's actual voice
// instead of a fixed pulse. It is invoked from miniaudio's capture thread, so it
// MUST be cheap and non-blocking (the UI wiring just pushes a throttled value).
var levelSink atomic.Value // func(float64)

// SetLevelSink installs (or clears, with nil) the per-frame RMS callback.
func SetLevelSink(fn func(float64)) {
	if fn == nil {
		levelSink.Store((func(float64))(nil))
		return
	}
	levelSink.Store(fn)
}

func emitLevel(rms float64) {
	if v := levelSink.Load(); v != nil {
		if fn, _ := v.(func(float64)); fn != nil {
			fn(rms)
		}
	}
}

const (
	sampleRate = 16000
	channels   = 1
	bitDepth   = 16

	// How long to wait for the user to start speaking before giving up. Generous
	// enough to cover reaction time on a slow machine, short enough that a
	// mis-trigger doesn't pin the microphone for the whole maxDuration.
	noSpeechTimeout = 4 * time.Second
)

func RecordDynamic(maxDuration time.Duration, silenceThreshold float64, silenceFramesNeeded int) ([]float32, error) {
	fmt.Printf("\n🔴 RECORDING (Speak now, will stop automatically upon silence)...\n")

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		// Suppress logs
	})
	if err != nil {
		return nil, fmt.Errorf("malgo init failed: %v", err)
	}
	defer func() {
		_ = ctx.Uninit()
		ctx.Free()
	}()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatF32
	deviceConfig.Capture.Channels = uint32(channels)
	deviceConfig.SampleRate = uint32(sampleRate)
	deviceConfig.Alsa.NoMMap = 1

	// pcmFloat32 and the counters below are written from malgo's capture thread
	// and read from the polling loop, so every shared field is guarded. The
	// buffer is copied out under the same lock once the device has stopped.
	var mu sync.Mutex
	var pcmFloat32 []float32
	var consecutiveSilence int
	var speechSeen bool

	// Define capture callback
	onRecvFrames := func(pOutputSample, pInputSamples []byte, framecount uint32) {
		sampleCount := int(framecount) * channels
		samples := make([]float32, sampleCount)

		// Convert byte array to float32 safely
		for i := 0; i < len(pInputSamples); i += 4 {
			bits := binary.LittleEndian.Uint32(pInputSamples[i:])
			samples[i/4] = math.Float32frombits(bits)
		}

		var sumSquares float64
		for _, sample := range samples {
			sumSquares += float64(sample * sample)
		}
		rms := math.Sqrt(sumSquares / float64(len(samples)))
		emitLevel(rms) // drive the live listening waveform (non-blocking)

		mu.Lock()
		pcmFloat32 = append(pcmFloat32, samples...)
		// Silence-detection is ARMED by speech, not by the start of the capture.
		// Counting silence from frame 0 meant the ~2s of ordinary reaction time
		// between the pill lighting up and the user actually speaking was itself
		// enough to trip the stop condition — the recording ended before a single
		// word landed, and the caller reported "audio too short".
		if rms >= silenceThreshold {
			speechSeen = true
			consecutiveSilence = 0
		} else if speechSeen {
			consecutiveSilence += int(framecount)
		}
		mu.Unlock()
	}

	captureCallbacks := malgo.DeviceCallbacks{
		Data: onRecvFrames,
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, captureCallbacks)
	if err != nil {
		return nil, fmt.Errorf("failed to init malgo device: %v", err)
	}
	defer device.Uninit()

	err = device.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start malgo capture: %v", err)
	}

	// Capture loop
	startTime := time.Now()
	for {
		time.Sleep(100 * time.Millisecond)

		if time.Since(startTime) > maxDuration {
			fmt.Println("\n⏳ Max duration reached. Stopping recording...")
			break
		}

		mu.Lock()
		silence, heardSpeech := consecutiveSilence, speechSeen
		mu.Unlock()

		if heardSpeech && silence >= silenceFramesNeeded {
			fmt.Println("\n🔇 Silence detected. Stopping recording...")
			break
		}

		// If nothing above the threshold has arrived at all, don't hold the mic
		// open for the full maxDuration — the user triggered by accident, or the
		// wrong capture device is selected. Bail out early with what we have so
		// the caller's "audio too short" path fires promptly instead of after 10s.
		if !heardSpeech && time.Since(startTime) > noSpeechTimeout {
			fmt.Println("\n🤐 No speech detected. Stopping recording...")
			break
		}
	}

	err = device.Stop()
	if err != nil {
		return nil, err
	}

	mu.Lock()
	out := make([]float32, len(pcmFloat32))
	copy(out, pcmFloat32)
	mu.Unlock()

	fmt.Println("⏹️  Finished Recording.")
	return out, nil
}

// putUint32LE writes a uint32 in little-endian format at the given position.
func putUint32LE(buf []byte, pos int, val uint32) {
	binary.LittleEndian.PutUint32(buf[pos:], val)
}

// putUint16LE writes a uint16 in little-endian format at the given position.
func putUint16LE(buf []byte, pos int, val uint16) {
	binary.LittleEndian.PutUint16(buf[pos:], val)
}

// Float32ToWav converts raw float32 PCM data (16000Hz, 16-bit, mono) to a valid WAV byte array.
func Float32ToWav(pcm []float32) ([]byte, error) {
	dataSize := len(pcm) * (bitDepth / 8)
	buf := make([]byte, 44+dataSize)

	// RIFF Header
	copy(buf[0:4], "RIFF")
	putUint32LE(buf, 4, uint32(36+dataSize))
	copy(buf[8:12], "WAVE")

	// fmt chunk
	copy(buf[12:16], "fmt ")
	putUint32LE(buf, 16, 16)                // chunk size
	putUint16LE(buf, 20, 1)                 // audio format (PCM)
	putUint16LE(buf, 22, uint16(channels))  // num channels
	putUint32LE(buf, 24, uint32(sampleRate)) // sample rate
	putUint32LE(buf, 28, uint32(sampleRate*channels*bitDepth/8)) // byte rate
	putUint16LE(buf, 32, uint16(channels*bitDepth/8))           // block align
	putUint16LE(buf, 34, uint16(bitDepth))                      // bits per sample

	// data chunk
	copy(buf[36:40], "data")
	putUint32LE(buf, 40, uint32(dataSize))

	// Convert float32 to int16 bytes
	offset := 44
	for _, f := range pcm {
		if f > 1.0 {
			f = 1.0
		}
		if f < -1.0 {
			f = -1.0
		}
		i16 := int16(f * 32767.0)
		binary.LittleEndian.PutUint16(buf[offset:], uint16(i16))
		offset += 2
	}

	return buf, nil
}
