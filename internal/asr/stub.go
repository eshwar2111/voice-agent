//go:build !whisper

// Default (no-voice) build: a stub ASR engine that links without whisper.cpp.
// Build the voice-enabled binary with `-tags whisper` (requires a whisper.cpp
// build whose C++ libs match your Go toolchain's GCC).
package asr

import "fmt"

// SetPaths is a no-op in the stub build (voice disabled).
func SetPaths(cliPath, modelPath string) {}

// Close is a no-op in the stub build.
func Close() {}

// Transcribe always errors in the stub build — voice was not compiled in.
func Transcribe(samples []float32) (string, error) {
	return "", fmt.Errorf("voice not available: build with -tags whisper to enable speech-to-text")
}

// TranscribeWAV always errors in the stub build.
func TranscribeWAV(wavBytes []byte) (string, error) {
	return "", fmt.Errorf("voice not available: build with -tags whisper to enable speech-to-text")
}
