package llm

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/config"
)

func NewProvider(cfg *config.Config) (Provider, error) {
	providerName := strings.ToLower(strings.TrimSpace(cfg.LLMProvider))
	switch providerName {
	case "gemini":
		return NewGemini(cfg.APIKey, cfg.Model), nil
	}

	if provider, ok := createProviderFromRegistry(providerName, cfg.APIKey, cfg.Model, cfg.BaseURL); ok {
		return provider, nil
	}

	available := append([]string{"gemini"}, AvailableProviders()...)
	return nil, fmt.Errorf("%w: %q (available: %s)", errors.New("unsupported provider"), cfg.LLMProvider, strings.Join(available, ", "))
}
