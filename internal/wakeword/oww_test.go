package wakeword

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// readWavInt16 parses a standard PCM16 mono WAV (finds the "data" chunk).
func readWavInt16(t *testing.T, path string) []int16 {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	// Scan chunks for "data".
	i := 12 // skip RIFF....WAVE
	for i+8 <= len(b) {
		id := string(b[i : i+4])
		sz := int(binary.LittleEndian.Uint32(b[i+4 : i+8]))
		body := i + 8
		if id == "data" {
			if body+sz > len(b) {
				sz = len(b) - body
			}
			n := sz / 2
			out := make([]int16, n)
			for k := 0; k < n; k++ {
				out[k] = int16(binary.LittleEndian.Uint16(b[body+2*k:]))
			}
			return out
		}
		i = body + sz + (sz & 1)
	}
	t.Fatal("no data chunk in wav")
	return nil
}

// TestOWWDetectsHeyJarvis is the golden test: the Go port must fire on a TTS
// "hey jarvis" clip, matching the openWakeWord Python reference (max ~0.998).
// Gated on the models + clip being present (fetched locally, gitignored).
func TestOWWDetectsHeyJarvis(t *testing.T) {
	dir := `E:\Voice Agent\models\wakeword`
	for _, f := range []string{"melspectrogram.onnx", "embedding_model.onnx", "hey_jarvis_v0.1.onnx", "hey_jarvis_test.wav"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Skipf("missing %s (fetch openWakeWord models to run this)", f)
		}
	}
	// Point onnxruntime at the working 1.27.1 DLL (avoid the stray System32 one).
	os.Setenv("ONNXRUNTIME_LIB_PATH", `E:\Voice Agent\onnxruntime.dll`)

	eng, err := newOWWEngine(
		filepath.Join(dir, "melspectrogram.onnx"),
		filepath.Join(dir, "embedding_model.onnx"),
		filepath.Join(dir, "hey_jarvis_v0.1.onnx"),
		owwThreshold,
	)
	if err != nil {
		t.Fatalf("newOWWEngine: %v", err)
	}
	defer eng.Close()
	o := eng.newDetector()

	audio := readWavInt16(t, filepath.Join(dir, "hey_jarvis_test.wav"))
	var maxP float32
	for i := 0; i+owwChunk <= len(audio); i += owwChunk {
		p, err := o.predict(audio[i : i+owwChunk])
		if err != nil {
			t.Fatalf("predict: %v", err)
		}
		if p > maxP {
			maxP = p
		}
	}
	t.Logf("max hey_jarvis probability = %.3f (reference ~0.998)", maxP)
	if maxP < 0.9 {
		t.Fatalf("expected a strong detection (>0.9) on the TTS 'hey jarvis' clip, got %.3f", maxP)
	}
}
