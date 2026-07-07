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
	"golang.org/x/oauth2/microsoft"
)

// Official Microsoft Client ID for this desktop application.
const BuiltInMicrosoftClientID = "4a3b2c1d-0e1f-2g3h-4i5j-6k7l8m9n0o1p"

type MicrosoftProvider struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
}

func NewMicrosoftProvider(cfg *config.Config) *MicrosoftProvider {
	return &MicrosoftProvider{
		ClientID:     cfg.MicrosoftClientID,
		ClientSecret: cfg.MicrosoftClientSecret,
		Scopes: []string{
			"openid",
			"profile",
			"email",
			"offline_access",
			"User.Read",
			"Mail.Read",
			"Mail.Send",
			"Calendars.ReadWrite",
			"Files.ReadWrite.All",
			"Files.Read.All",
			"Sites.ReadWrite.All",
		},
	}
}

func (m *MicrosoftProvider) GetAuthURL(state, challenge string) string {
	return fmt.Sprintf(
		"https://login.microsoftonline.com/common/oauth2/v2.0/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&code_challenge=%s&code_challenge_method=S256&state=%s",
		m.ClientID,
		url.QueryEscape(RedirectURI),
		url.QueryEscape(strings.Join(m.Scopes, " ")),
		challenge,
		state,
	)
}

func (m *MicrosoftProvider) ExchangeCode(ctx context.Context, code, verifier string) (*oauth2.Token, error) {
	conf := &oauth2.Config{
		ClientID:     m.ClientID,
		ClientSecret: m.ClientSecret,
		RedirectURL:  RedirectURI,
		Endpoint:     microsoft.AzureADEndpoint("common"),
		Scopes:       m.Scopes,
	}

	return conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
}

type MicrosoftUserInfo struct {
	Email string `json:"mail"`
}

func GetMicrosoftUserInfo(ctx context.Context, cfg *config.Config) (*MicrosoftUserInfo, error) {
	client, err := GetMicrosoftClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	resp, err := client.Get("https://graph.microsoft.com/v1.0/me")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get user info: %s", resp.Status)
	}

	var info struct {
		Mail            string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	email := info.Mail
	if email == "" {
		email = info.UserPrincipalName
	}

	return &MicrosoftUserInfo{Email: email}, nil
}

func (m *MicrosoftProvider) GetTokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	conf := &oauth2.Config{
		ClientID:     m.ClientID,
		ClientSecret: m.ClientSecret,
		RedirectURL:  RedirectURI,
		Endpoint:     microsoft.AzureADEndpoint("common"),
		Scopes:       m.Scopes,
	}
	return conf.TokenSource(ctx, token)
}

// GetMicrosoftClient returns an authenticated HTTP client for Microsoft Graph APIs
func GetMicrosoftClient(ctx context.Context, cfg *config.Config) (*http.Client, error) {
	store := NewTokenStore("config.json")
	token, err := store.LoadToken(ProviderMicrosoft, cfg)
	if err != nil {
		return nil, fmt.Errorf("Microsoft account not linked: %w", err)
	}

	p := NewMicrosoftProvider(cfg)
	tokenSource := p.GetTokenSource(ctx, token)
	
	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get valid Microsoft token: %w", err)
	}

	if newToken.AccessToken != token.AccessToken {
		if err := store.SaveToken(ProviderMicrosoft, newToken, cfg); err != nil {
			fmt.Printf("Warning: failed to save refreshed Microsoft token: %v\n", err)
		}
	}

	return oauth2.NewClient(ctx, tokenSource), nil
}

// StartMicrosoftAuth initiates the Microsoft OAuth flow
func StartMicrosoftAuth(ctx context.Context, cfg *config.Config) error {
	state := GenerateStateOauthCookie()
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return fmt.Errorf("failed to generate PKCE: %w", err)
	}

	p := NewMicrosoftProvider(cfg)
	if p.ClientID == "" {
		return fmt.Errorf("Microsoft Client ID is missing from config.json")
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
		token, err := p.ExchangeCode(ctx, code, verifier)
		if err != nil {
			return fmt.Errorf("failed to exchange code: %w", err)
		}

		store := NewTokenStore("config.json")
		if err := store.SaveToken(ProviderMicrosoft, token, cfg); err != nil {
			return fmt.Errorf("failed to save Microsoft token: %w", err)
		}
		return nil
	case err := <-errorChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
