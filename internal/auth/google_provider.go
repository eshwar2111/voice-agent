package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"

	"github.com/yourname/voice-agent/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Official Google Client ID for this desktop application.
const BuiltInGoogleClientID = "1019943584442-l0acjt0of5ms6dub3jjk61dc4e71m149.apps.googleusercontent.com"

type GoogleProvider struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
}

func NewGoogleProvider(cfg *config.Config) *GoogleProvider {
	return &GoogleProvider{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		Scopes: []string{
			"openid",
			"email",
			"profile",
			"https://www.googleapis.com/auth/calendar",
			"https://www.googleapis.com/auth/gmail.modify",
			"https://www.googleapis.com/auth/gmail.send",
			"https://www.googleapis.com/auth/drive.readonly",
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/documents",
			"https://www.googleapis.com/auth/spreadsheets",
			"https://www.googleapis.com/auth/presentations",
			"https://www.googleapis.com/auth/drive.metadata.readonly",
		},
	}
}

func (g *GoogleProvider) GetAuthURL(state, challenge string) string {
	return fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&code_challenge=%s&code_challenge_method=S256&state=%s&access_type=offline&prompt=consent",
		g.ClientID,
		url.QueryEscape(RedirectURI),
		url.QueryEscape(strings.Join(g.Scopes, " ")),
		challenge,
		state,
	)
}

func (g *GoogleProvider) ExchangeCode(ctx context.Context, code, verifier string) (*oauth2.Token, error) {
	conf := &oauth2.Config{
		ClientID:     g.ClientID,
		ClientSecret: g.ClientSecret,
		RedirectURL:  RedirectURI,
		Endpoint:     google.Endpoint,
		Scopes:       g.Scopes,
	}

	// Use PKCE verifier
	return conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
}

func (g *GoogleProvider) GetTokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	conf := &oauth2.Config{
		ClientID:     g.ClientID,
		ClientSecret: g.ClientSecret,
		RedirectURL:  RedirectURI,
		Endpoint:     google.Endpoint,
		Scopes:       g.Scopes,
	}
	return conf.TokenSource(ctx, token)
}

// GetGoogleClient returns an authenticated HTTP client for Google APIs
func GetGoogleClient(ctx context.Context, cfg *config.Config) (*http.Client, error) {
	store := NewTokenStore("config.json")
	token, err := store.LoadToken(ProviderGoogle, cfg)
	if err != nil {
		return nil, fmt.Errorf("Google account not linked: %w", err)
	}

	p := NewGoogleProvider(cfg)
	tokenSource := p.GetTokenSource(ctx, token)
	
	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get valid token: %w", err)
	}

	if newToken.AccessToken != token.AccessToken {
		if err := store.SaveToken(ProviderGoogle, newToken, cfg); err != nil {
			fmt.Printf("Warning: failed to save refreshed Google token: %v\n", err)
		}
	}

	return oauth2.NewClient(ctx, tokenSource), nil
}

// StartGoogleAuth initiates the Google OAuth flow with PKCE
func StartGoogleAuth(ctx context.Context, cfg *config.Config) error {
	state := GenerateStateOauthCookie()
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return fmt.Errorf("failed to generate PKCE: %w", err)
	}

	p := NewGoogleProvider(cfg)
	if p.ClientID == "" {
		return fmt.Errorf("Google Client ID is missing from config.json")
	}
	
	authURL := p.GetAuthURL(state, challenge)

	resultChan := make(chan string)
	errorChan := make(chan error)

	StartCallbackServer(8787, state, resultChan, errorChan)

	err = exec.Command("rundll32", "url.dll,FileProtocolHandler", authURL).Start()
	if err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	select {
	case code := <-resultChan:
		fmt.Println("📩 Received code from callback, exchanging for token...")
		token, err := p.ExchangeCode(ctx, code, verifier)
		if err != nil {
			fmt.Printf("❌ Token exchange failed: %v\n", err)
			return fmt.Errorf("failed to exchange code: %w", err)
		}

		fmt.Println("✅ Token exchange successful. Saving token...")
		store := NewTokenStore("config.json")
		if err := store.SaveToken(ProviderGoogle, token, cfg); err != nil {
			fmt.Printf("❌ Failed to save token: %v\n", err)
			return fmt.Errorf("failed to save token: %w", err)
		}
		fmt.Println("🎉 Google account linked successfully!")
		return nil
	case err := <-errorChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type GoogleUserInfo struct {
	Email string `json:"email"`
}

func GetGoogleUserInfo(ctx context.Context, cfg *config.Config) (*GoogleUserInfo, error) {
	client, err := GetGoogleClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get user info: %s", resp.Status)
	}

	var info GoogleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return &info, nil
}
