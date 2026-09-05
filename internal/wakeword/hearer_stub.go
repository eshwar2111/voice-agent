//go:build !whisper

package wakeword

import "github.com/yourname/voice-agent/internal/audio"

// NewKeywordHearer is a no-op without the whisper build tag (no Porcupine): the
// caller falls back to open-mic-only barge-in.
func NewKeywordHearer(accessKey string) (audio.KeywordHearer, func(), error) {
	return nil, func() {}, nil
}
