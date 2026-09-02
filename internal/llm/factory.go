package llm

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/yourname/voice-agent/config"
)

// NewProvider builds the LLM provider from config. Any registered provider works
// (gemini, openai, anthropic/claude, openrouter, groq, together, ollama,
// lmstudio, local, custom) — the code depends only on the Provider interface, so
// swapping providers is pure config. When a fallback_provider is set, the primary
// is wrapped so a failure (e.g. a 429 quota wall) transparently retries on it.
func NewProvider(cfg *config.Config) (Provider, error) {
	primary, err := buildProvider(cfg.LLMProvider, cfg.APIKey, cfg.Model, cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	if fb := strings.TrimSpace(cfg.FallbackProvider); fb != "" {
		fallback, ferr := buildProvider(cfg.FallbackProvider, cfg.FallbackAPIKey, cfg.FallbackModel, "")
		if ferr != nil {
			log.Printf("[llm] fallback provider %q not available (%v) — running without failover", fb, ferr)
			return primary, nil
		}
		log.Printf("[llm] provider=%s with fallback=%s", cfg.LLMProvider, fb)
		return NewFallbackProvider(primary, fallback), nil
	}
	return primary, nil
}

// buildProvider resolves one provider by name (gemini is built-in; everything
// else comes from the registry, so new providers register themselves via init()).
func buildProvider(name, key, model, baseURL string) (Provider, error) {
	providerName := strings.ToLower(strings.TrimSpace(name))
	if providerName == "gemini" {
		return NewGemini(key, model), nil
	}
	if provider, ok := createProviderFromRegistry(providerName, key, model, baseURL); ok {
		return provider, nil
	}
	available := append([]string{"gemini"}, AvailableProviders()...)
	return nil, fmt.Errorf("%w: %q (available: %s)", errors.New("unsupported provider"), name, strings.Join(available, ", "))
}
