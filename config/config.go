package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	// SpeakResponses toggles TTS output for agent responses. Defaults to
	// true when the key is absent from config.json.
	SpeakResponses bool `json:"speak_responses"`

	// File-intelligence index (internal/fileindex).
	//
	// IndexRoots are the directories the index scans + watches. When absent it
	// defaults to the user's Documents/Desktop/Downloads/Projects folders.
	// IndexExclude names are merged with the built-in excludes (node_modules,
	// AppData, .git, etc.) inside fileindex. SemanticSearch gates the lazy local
	// ONNX BGE fallback and defaults to true when the key is absent. BGEModelPath
	// and BGEVocabPath point at the BGE-small ONNX model + vocab; when empty (or
	// missing on disk) semantic search disables cleanly and Tiers 1–2 still work.
	IndexRoots     []string `json:"index_roots"`
	IndexExclude   []string `json:"index_exclude"`
	SemanticSearch bool     `json:"semantic_search"`
	BGEModelPath   string   `json:"bge_model_path"`
	BGEVocabPath   string   `json:"bge_vocab_path"`
}

// DefaultIndexRoots returns the file-index scan roots relative to the user's
// home directory, used when config.json omits index_roots.
func DefaultIndexRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("USERPROFILE")
	}
	subs := []string{"Documents", "Desktop", "Downloads", "Projects"}
	roots := make([]string, 0, len(subs))
	for _, s := range subs {
		if home == "" {
			roots = append(roots, s)
			continue
		}
		roots = append(roots, filepath.Join(home, s))
	}
	return roots
}

// DefaultBGEModelPath / DefaultBGEVocabPath are the conventional on-disk
// locations for the BGE-small assets under models/.
const (
	DefaultBGEModelPath = "models/bge-small-en-v1.5/model.onnx"
	DefaultBGEVocabPath = "models/bge-small-en-v1.5/vocab.txt"
)

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
		if _, ok := raw["speak_responses"]; !ok {
			cfg.SpeakResponses = true
		}
		// SemanticSearch, like the two toggles above, defaults to true only when
		// the key is absent — an explicit false must be honored.
		if _, ok := raw["semantic_search"]; !ok {
			cfg.SemanticSearch = true
		}
	}

	// IndexRoots default to the user's common folders when unspecified.
	if len(cfg.IndexRoots) == 0 {
		cfg.IndexRoots = DefaultIndexRoots()
	}
	if cfg.BGEModelPath == "" {
		cfg.BGEModelPath = DefaultBGEModelPath
	}
	if cfg.BGEVocabPath == "" {
		cfg.BGEVocabPath = DefaultBGEVocabPath
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
