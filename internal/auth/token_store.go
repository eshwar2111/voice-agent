package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/config"
	"golang.org/x/oauth2"
)

type TokenStore struct {
	ConfigPath string
}

func NewTokenStore(configPath string) *TokenStore {
	return &TokenStore{
		ConfigPath: configPath,
	}
}

func (s *TokenStore) SaveToken(provider ProviderType, token *oauth2.Token, cfg *config.Config) error {
	tokenData, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("failed to marshal token: %w", err)
	}

	encrypted, err := config.Encrypt(tokenData)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(encrypted)

	switch provider {
	case ProviderGoogle:
		cfg.GoogleToken = encoded
	case ProviderMicrosoft:
		cfg.MicrosoftToken = encoded
	case ProviderSpotify:
		cfg.SpotifyToken = encoded
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	err = config.SaveConfig(s.ConfigPath, cfg)
	if err != nil {
		fmt.Printf("❌ Token Store: Failed to save %s token to %s: %v\n", provider, s.ConfigPath, err)
	} else {
		fmt.Printf("🔑 Token Store: Successfully saved %s token to %s\n", provider, s.ConfigPath)
	}
	return err
}

func (s *TokenStore) LoadToken(provider ProviderType, cfg *config.Config) (*oauth2.Token, error) {
	var encoded string
	switch provider {
	case ProviderGoogle:
		encoded = cfg.GoogleToken
	case ProviderMicrosoft:
		encoded = cfg.MicrosoftToken
	case ProviderSpotify:
		encoded = cfg.SpotifyToken
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}

	if encoded == "" {
		return nil, fmt.Errorf("no token found for provider: %s", provider)
	}

	encrypted, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode token: %w", err)
	}

	decrypted, err := config.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt token: %w", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(decrypted, &token); err != nil {
		return nil, fmt.Errorf("failed to unmarshal token: %w", err)
	}

	return &token, nil
}
