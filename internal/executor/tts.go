package executor

import (
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
)

var (
	ttsMu       sync.Mutex
	ttsCmd      *exec.Cmd
	isSpeaking  atomic.Bool // true while TTS process is running
	AbortStream atomic.Bool // set true to stop streaming sentences
)

// BargeWatch, when set, enables barge-in: while the agent speaks, it is run in a
// goroutine and must BLOCK until ctx is cancelled (speech finished normally) or
// it detects the user interrupting — at which point it calls the stop func it is
// given (StopSpeaking). main wires this to audio.WatchForBargeIn with the
// configured keyword detector; nil leaves speech uninterruptible (unchanged
// legacy behavior). It runs async, so it never delays the start of speech.
var BargeWatch func(ctx context.Context, stop func())

// IsSpeaking returns true if TTS is currently active.
// Used by the listen loop to avoid recording our own voice output.
func IsSpeaking() bool {
	return isSpeaking.Load()
}

// StopSpeaking kills any active TTS process immediately and aborts active streams.
func StopSpeaking() {
	ttsMu.Lock()
	defer ttsMu.Unlock()
	if ttsCmd != nil && ttsCmd.Process != nil {
		_ = ttsCmd.Process.Kill()
		ttsCmd = nil
	}
	isSpeaking.Store(false)

	// Abort stream processing
	AbortStream.Store(true)
}

// Speak executes local text-to-speech using native Windows SAPI synchronously.
// It blocks until speech is finished or StopSpeaking() is called.
func Speak(text string) error {
	// If already speaking, stop previous speech first
	StopSpeaking()

	fmt.Printf("🎙️ Speaking: '%s'\n", text)

	b64Text := base64.StdEncoding.EncodeToString([]byte(text))

	// Use SpeakAsync=false so the process stays alive until audio finishes.
	// We own the process handle so StopSpeaking() can kill it at any time.
	psScript := fmt.Sprintf(`
Add-Type -AssemblyName System.Speech;
$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer;
$synth.Rate = 0;
$bytes = [System.Convert]::FromBase64String("%s");
$decoded = [System.Text.Encoding]::UTF8.GetString($bytes);
$synth.Speak($decoded);
`, b64Text)

	ttsMu.Lock()
	cmd := exec.Command("powershell", "-NoProfile", "-Command", psScript)
	ttsCmd = cmd
	ttsMu.Unlock()

	isSpeaking.Store(true)

	// Barge-in: watch the mic for the user interrupting while this utterance
	// plays. The watcher is cancelled the instant speech ends, so it never
	// lingers into the next listen. Started async — no added speech latency.
	var stopWatch context.CancelFunc
	if BargeWatch != nil {
		var wctx context.Context
		wctx, stopWatch = context.WithCancel(context.Background())
		go BargeWatch(wctx, StopSpeaking)
	}

	err := cmd.Run()
	isSpeaking.Store(false)
	if stopWatch != nil {
		stopWatch()
	}

	ttsMu.Lock()
	ttsCmd = nil
	ttsMu.Unlock()

	return err
}
