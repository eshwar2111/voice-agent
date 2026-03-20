package audio

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/gen2brain/malgo"
)

const (
	sampleRate = 16000
	channels   = 1
	bitDepth   = 16
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

	var pcmFloat32 []float32
	var consecutiveSilence int

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
			pcmFloat32 = append(pcmFloat32, sample)
			sumSquares += float64(sample * sample)
		}

		rms := math.Sqrt(sumSquares / float64(len(samples)))
		if rms < silenceThreshold {
			consecutiveSilence += int(framecount)
		} else {
			consecutiveSilence = 0
		}
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

		if consecutiveSilence >= silenceFramesNeeded {
			fmt.Println("\n🔇 Silence detected. Stopping recording...")
			break
		}
	}

	err = device.Stop()
	if err != nil {
		return nil, err
	}

	fmt.Println("⏹️  Finished Recording.")
	return pcmFloat32, nil
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
