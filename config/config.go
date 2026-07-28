package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Config struct {
	LLMProvider        string `json:"llm_provider"`
	APIKey             string `json:"api_key"`
	Model              string `json:"model"`
	BaseURL            string `json:"base_url"`
	FallbackProvider   string `json:"fallback_provider"`
	FallbackAPIKey     string `json:"fallback_api_key"`
	FallbackModel      string `json:"fallback_model"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	PorcupineAccessKey string `json:"porcupine_access_key"`
	WhisperPath        string `json:"whisper_path"`
	WhisperModel       string `json:"whisper_model"`

	// UX Toggles
	EnableVoice     bool `json:"enable_voice"`
	PrivacyMode     bool `json:"privacy_mode"`
	EnableProactive bool `json:"enable_proactive"`

	// OAuth Credentials
	GoogleClientID        string `json:"google_client_id"`
	GoogleClientSecret    string `json:"google_client_secret"`
	MicrosoftClientID     string `json:"microsoft_client_id"`
	MicrosoftClientSecret string `json:"microsoft_client_secret"`
	SpotifyClientID       string `json:"spotify_client_id"`
	SpotifyClientSecret   string `json:"spotify_client_secret"`

	// OAuth Tokens (Encrypted)
	GoogleToken    string `json:"google_token"`
	MicrosoftToken string `json:"microsoft_token"`
	SpotifyToken   string `json:"spotify_token"`

	// TrustedExecution gates plans through the internal/trust layer (risk
	// classification, one-shot approval gate, verification, recovery ladder).
	// Defaults to true when the key is absent from config.json.
	TrustedExecution bool `json:"trusted_execution"`
}

// defaults applied when fields are missing or invalid.
const (
	DefaultTimeoutSeconds = 30
	DefaultLLMProvider    = "gemini"
	DefaultModel          = "gemini-2.5-flash"
)

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read file: %w", err)
	}
	return loadFromBytes(data)
}

// loadFromBytes parses config JSON and applies defaults. Factored out of
// LoadConfig so tests can exercise default logic without touching the disk.
func loadFromBytes(data []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse JSON: %w", err)
	}

	// Apply defaults for missing/invalid values
	if cfg.LLMProvider == "" {
		cfg.LLMProvider = DefaultLLMProvider
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = DefaultTimeoutSeconds
	}

	// TrustedExecution defaults to true when the key is absent. A plain
	// bool would default to false, so detect key presence by re-unmarshalling
	// into a raw map and only override when the key was not supplied.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		if _, ok := raw["trusted_execution"]; !ok {
			cfg.TrustedExecution = true
		}
	}

	return &cfg, nil
}

// Timeout returns the configured timeout as a time.Duration.
func (c *Config) Timeout() time.Duration {
	if c.TimeoutSeconds <= 0 {
		return DefaultTimeoutSeconds * time.Second
	}
	return time.Duration(c.TimeoutSeconds) * time.Second
}

func SaveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal JSON: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
