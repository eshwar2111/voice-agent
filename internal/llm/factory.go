package llm

import (
	"errors"

	"github.com/yourname/voice-agent/config"
)

func NewProvider(cfg *config.Config) (Provider, error) {
	switch cfg.LLMProvider {
	case "gemini":
		return NewGemini(cfg.APIKey, cfg.Model), nil
	default:
		return nil, errors.New("unsupported provider")
	}
}
