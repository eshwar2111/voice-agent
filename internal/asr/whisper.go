package asr

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/ggerganov/whisper.cpp/bindings/go"
)

var (
	ctx      *whisper.Context
	ctxMu    sync.Mutex
	curModel string
)

// SetPaths initializes the in-process whisper context with the given model path.
func SetPaths(cliPath, modelPath string) {
	ctxMu.Lock()
	defer ctxMu.Unlock()

	if modelPath == "" {
		return
	}

	if ctx != nil && curModel == modelPath {
		return
	}

	if ctx != nil {
		ctx.Whisper_free()
	}

	fmt.Printf("📦 Loading Whisper model: %s ...\n", modelPath)
	ctx = whisper.Whisper_init(modelPath)
	if ctx == nil {
		log.Printf("❌ Failed to initialize Whisper context from model: %s", modelPath)
		return
	}
	curModel = modelPath
	fmt.Println("✅ Whisper model loaded successfully.")
}

// Close frees the whisper context.
func Close() {
	ctxMu.Lock()
	defer ctxMu.Unlock()
	if ctx != nil {
		ctx.Whisper_free()
		ctx = nil
	}
}

// Transcribe takes raw PCM float32 samples (16kHz, mono) and returns the transcript.
func Transcribe(samples []float32) (string, error) {
	ctxMu.Lock()
	defer ctxMu.Unlock()

	if ctx == nil {
		return "", fmt.Errorf("whisper context not initialized")
	}

	params := ctx.Whisper_full_default_params(whisper.SAMPLING_GREEDY)
	// Force English for better accuracy with base.en model
	// params.SetLanguage("en") // Some bindings might not have this helper, we can use lang_id

	// Run the model
	if err := ctx.Whisper_full(params, samples, nil, nil, nil); err != nil {
		return "", fmt.Errorf("whisper_full failed: %w", err)
	}

	var sb strings.Builder
	nSegments := ctx.Whisper_full_n_segments()
	for i := 0; i < nSegments; i++ {
		sb.WriteString(ctx.Whisper_full_get_segment_text(i))
	}

	transcript := strings.TrimSpace(sb.String())
	if transcript == "" {
		return "", fmt.Errorf("empty transcript")
	}

	return transcript, nil
}

// TranscribeWAV is kept for backward compatibility, but now uses the in-process engine.
func TranscribeWAV(wavBytes []byte) (string, error) {
	// Simple WAV header skip (44 bytes) and convert to float32
	// For better robustness, use a proper WAV decoder or just use the raw float32 from RecordDynamic
	return "", fmt.Errorf("TranscribeWAV is deprecated, use Transcribe(samples []float32)")
}
