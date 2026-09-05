//go:build whisper

package wakeword

import (
	porcupine "github.com/Picovoice/porcupine/binding/go/v3"

	"github.com/yourname/voice-agent/internal/audio"
)

// kwHearer adapts a Porcupine instance to audio.KeywordHearer so the SAME "wake
// word" doubles as the guaranteed barge-in interrupt: saying "Porcupine" while
// the agent is speaking halts it, even over speakers where echo makes the
// energy detector deliberately conservative. It is fed frames captured by the
// barge watcher, so it needs no recorder of its own.
type kwHearer struct{ p *porcupine.Porcupine }

func (h *kwHearer) Hear(frame []int16) bool {
	if h == nil || h.p == nil {
		return false
	}
	idx, err := h.p.Process(frame)
	return err == nil && idx >= 0
}

// NewKeywordHearer builds a Porcupine "Porcupine" detector for barge-in. The
// returned closer releases it. Returns (nil, nil, err) if Porcupine can't
// initialize, so the caller falls back to open-mic-only barge-in.
func NewKeywordHearer(accessKey string) (audio.KeywordHearer, func(), error) {
	p := &porcupine.Porcupine{
		AccessKey:       accessKey,
		BuiltInKeywords: []porcupine.BuiltInKeyword{porcupine.PORCUPINE},
	}
	if err := p.Init(); err != nil {
		return nil, nil, err
	}
	return &kwHearer{p: p}, func() { _ = p.Delete() }, nil
}
