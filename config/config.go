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
	TimeoutSeconds     int    `json:"timeout_seconds"`
	PorcupineAccessKey string `json:"porcupine_access_key"`
	WhisperPath        string `json:"whisper_path"`
	WhisperModel       string `json:"whisper_model"`
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
