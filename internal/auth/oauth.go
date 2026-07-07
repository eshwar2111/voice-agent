package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"

	"golang.org/x/oauth2"
)

// ProviderType represents the supported OAuth2 providers
type ProviderType string

const (
	ProviderGoogle    ProviderType = "google"
	ProviderMicrosoft ProviderType = "microsoft"
	ProviderSpotify   ProviderType = "spotify"

	// Production Redirect URI
	RedirectURI = "http://localhost:8787/callback"
)

// OAuthProvider defines the common interface for different OAuth2 providers
type OAuthProvider interface {
	GetAuthURL(state, challenge string) string
	ExchangeCode(ctx context.Context, code, verifier string) (*oauth2.Token, error)
	GetTokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource
}

// GetOAuthClient returns an authenticated HTTP client for a provider
func GetOAuthClient(ctx context.Context, provider OAuthProvider, token *oauth2.Token) *http.Client {
	return oauth2.NewClient(ctx, provider.GetTokenSource(ctx, token))
}

// GenerateStateOauthCookie creates a random state string for CSRF protection
func GenerateStateOauthCookie() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
