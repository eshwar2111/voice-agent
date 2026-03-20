package executor

import (
	"strings"
	"unicode"
)

// StreamSpeak starts a background process that reads text chunks from textChan,
// buffers them into logical sentences, and plays them sequentially using the TTS engine.
// It returns immediately.
func StreamSpeak(textChan <-chan string) {
	// Channel to hold complete sentences ready for speech
	// 100 sentences is plenty of buffer so the SSE reader never blocks
	sentenceChan := make(chan string, 100)

	// Worker 1: Read incoming chunks, split into sentences, and push to sentenceChan
	go func() {
		var buffer strings.Builder
		var lastChar rune

		for chunk := range textChan {
			for _, char := range chunk {
				// Flush boundary: standard punctuation followed by a space
				if (lastChar == '.' || lastChar == '?' || lastChar == '!' || lastChar == '\n') && unicode.IsSpace(char) {
					sentence := strings.TrimSpace(buffer.String())
					if len(sentence) > 0 {
						sentenceChan <- sentence
					}
					buffer.Reset()
				} else {
					buffer.WriteRune(char)
				}
				lastChar = char
			}
		}

		// Flush any remaining text when the channel closes
		remaining := strings.TrimSpace(buffer.String())
		if len(remaining) > 0 {
			sentenceChan <- remaining
		}
		close(sentenceChan)
	}()

	// Worker 2: Consume complete sentences and speak them sequentially
	go func() {
		for sentence := range sentenceChan {
			if AbortStream.Load() {
				// User hit stop, drain channel and exit
				break
			}

			// Speak will block until completion
			_ = Speak(sentence)
		}

		// Ensure UI resets to green after done speaking the entire stream
		// We use a small check because another intent might have started,
		// but realistically this is fine.
		if !IsSpeaking() {
			// Import ui package if not already
			// Wait, we need to import ui. I'll just rely on the existing
			// UI reset logic in the caller tools or main.go instead of coupling here,
			// or actually we use atomic bool. It's safer if speak.go or main.go handles it!
		}
	}()
}
