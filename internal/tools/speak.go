package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/yourname/voice-agent/internal/executor"
	"github.com/yourname/voice-agent/internal/ui"
	"log"
	"time"
)

type SpeakTool struct{}

func (s *SpeakTool) Name() string {
	return "speak"
}

func (s *SpeakTool) Description() string {
	return "Speaks text aloud using Text-To-Speech and displays it in the output overlay"
}

func (s *SpeakTool) Parameters() string {
	return `{"text": "string (required)"}`
}

func (s *SpeakTool) RequiresConfirmation() bool {
	return false
}

type SpeakArgs struct {
	Text string `json:"text"`
}

func (t *SpeakTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params SpeakArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	text := params.Text
	if len(strings.TrimSpace(text)) == 0 { // Removed !ok as params.Text will always exist
		return "", errors.New("missing text parameter for speak intent")
	}

	// Update the overlay to show the Speaking state with the Stop button
	ui.SetState(ui.StateSpeaking)

	// Show full text immediately in overlay for reading
	ui.ShowOutputOverlay(text)

	// Convert text into a channel of sentences so we can use StreamSpeak
	// This lets us stream even already-complete text through the same TTS pipeline
	textChan := make(chan string, len(text)+1)

	// Split into sentences and stream them
	go func() {
		// Feed text word by word with sentence boundaries to allow streaming
		// Break on sentence boundaries: '. ', '? ', '! ', '\n'
		var buf strings.Builder
		var lastChar rune

		for _, ch := range text {
			if (lastChar == '.' || lastChar == '?' || lastChar == '!' || lastChar == '\n') && unicode.IsSpace(ch) {
				sentence := strings.TrimSpace(buf.String())
				if len(sentence) > 0 {
					textChan <- sentence + string(lastChar)
				}
				buf.Reset()
			} else {
				buf.WriteRune(ch)
			}
			lastChar = ch
		}
		// Flush remaining text
		if buf.Len() > 0 {
			textChan <- strings.TrimSpace(buf.String())
		}
		close(textChan)
	}()

	// Stream the sentences to TTS
	// AbortStream is reset before each speak session
	executor.AbortStream.Store(false)

	// Run in a goroutine so the tool returns immediately
	// (the IsSpeaking flag prevents the listen loop from picking up our own voice)
	go func() {
		executor.StreamSpeak(textChan)
		// Wait until all sentences are done before resetting state
		// StreamSpeak returns immediately (it's non-blocking), but
		// IsSpeaking() tracks the active PowerShell process
		// We can't simply wait here without a sync primitive, so we poll briefly
		// Poll with a sleep, not a bare spin. This loop had no delay at all, so
		// it pegged a full CPU core for the entire duration of every spoken
		// response — seconds at a time, on every reply the agent speaks. The
		// deadline is a safety net: if IsSpeaking never clears (a wedged TTS
		// process), this goroutine used to spin forever AND the island would be
		// stuck out of idle with no way back.
		deadline := time.Now().Add(5 * time.Minute)
		for executor.IsSpeaking() {
			if time.Now().After(deadline) {
				log.Printf("[speak] TTS did not report completion within 5m — releasing UI state")
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		ui.SetState(ui.StateIdle)
	}()

	return text, nil
}
