package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/yourname/voice-agent/config"
	"golang.org/x/oauth2"
)

const (
	SpotifyAuthURL  = "https://accounts.spotify.com/authorize"
	SpotifyTokenURL = "https://accounts.spotify.com/api/token"
	SpotifyAPIBase  = "https://api.spotify.com/v1"
)

type SpotifyProvider struct {
	ClientID     string
	ClientSecret string
	Scopes       []string
}

func NewSpotifyProvider(cfg *config.Config) *SpotifyProvider {
	return &SpotifyProvider{
		ClientID:     cfg.SpotifyClientID,
		ClientSecret: cfg.SpotifyClientSecret,
		Scopes: []string{
			"user-read-private",
			"user-read-email",
			"user-read-playback-state",
			"user-modify-playback-state",
			"user-read-currently-playing",
			"user-library-read",
			"user-top-read",
			"user-read-recently-played",
			"playlist-read-private",
			"playlist-read-collaborative",
			"playlist-modify-private",
			"playlist-modify-public",
		},
	}
}

func (s *SpotifyProvider) GetAuthURL(state, challenge string) string {
	return fmt.Sprintf(
		"https://accounts.spotify.com/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s&show_dialog=true",
		s.ClientID,
		url.QueryEscape(RedirectURI),
		url.QueryEscape(strings.Join(s.Scopes, " ")),
		state,
	)
}

func (s *SpotifyProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", RedirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST", SpotifyTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.ClientID, s.ClientSecret)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Spotify token exchange failed (%s): %s", resp.Status, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	return &oauth2.Token{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		Expiry:       time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		RefreshToken: tokenResp.RefreshToken,
	}, nil
}

func (s *SpotifyProvider) GetTokenSource(ctx context.Context, token *oauth2.Token) oauth2.TokenSource {
	return &spotifyTokenSource{
		ctx:    ctx,
		client: s.ClientID,
		secret: s.ClientSecret,
		token:  token,
	}
}

type spotifyTokenSource struct {
	ctx    context.Context
	client string
	secret string
	token  *oauth2.Token
}

func (s *spotifyTokenSource) Token() (*oauth2.Token, error) {
	if s.token.Valid() && s.token.Expiry.After(time.Now()) {
		return s.token, nil
	}

	if s.token.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available, re-authentication required")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", s.token.RefreshToken)
	data.Set("client_id", s.client)

	req, err := http.NewRequestWithContext(s.ctx, "POST", SpotifyTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.client, s.secret)

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Spotify token refresh failed (%s): %s", resp.Status, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token refresh response: %w", err)
	}

	s.token.AccessToken = tokenResp.AccessToken
	s.token.TokenType = tokenResp.TokenType
	s.token.Expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	return s.token, nil
}

func GetSpotifyClient(ctx context.Context, cfg *config.Config) (*http.Client, error) {
	store := NewTokenStore("config.json")
	token, err := store.LoadToken(ProviderSpotify, cfg)
	if err != nil {
		return nil, fmt.Errorf("Spotify account not linked: %w", err)
	}

	p := NewSpotifyProvider(cfg)
	tokenSource := p.GetTokenSource(ctx, token)

	newToken, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get valid Spotify token: %w", err)
	}

	if newToken.AccessToken != token.AccessToken {
		if err := store.SaveToken(ProviderSpotify, newToken, cfg); err != nil {
			fmt.Printf("Warning: failed to save refreshed Spotify token: %v\n", err)
		}
	}

	return oauth2.NewClient(ctx, tokenSource), nil
}

func StartSpotifyAuth(ctx context.Context, cfg *config.Config) error {
	state := GenerateStateOauthCookie()
	p := NewSpotifyProvider(cfg)

	if p.ClientID == "" {
		return fmt.Errorf("Spotify Client ID is missing from config.json")
	}

	authURL := p.GetAuthURL(state, "")

	resultChan := make(chan string)
	errorChan := make(chan error)

	StartCallbackServer(8787, state, resultChan, errorChan)

	err := exec.Command("rundll32", "url.dll,FileProtocolHandler", authURL).Start()
	if err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	select {
	case code := <-resultChan:
		token, err := p.ExchangeCode(ctx, code)
		if err != nil {
			return fmt.Errorf("failed to exchange code: %w", err)
		}

		store := NewTokenStore("config.json")
		if err := store.SaveToken(ProviderSpotify, token, cfg); err != nil {
			return fmt.Errorf("failed to save Spotify token: %w", err)
		}
		return nil
	case err := <-errorChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type SpotifyUserInfo struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Country     string `json:"country"`
	Product     string `json:"product"` // free, premium
}

func GetSpotifyUserInfo(ctx context.Context, cfg *config.Config) (*SpotifyUserInfo, error) {
	client, err := GetSpotifyClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	resp, err := client.Get(SpotifyAPIBase + "/me")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get Spotify user info: %s", resp.Status)
	}

	var info SpotifyUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	return &info, nil
}

func SpotifyAPIRequest(ctx context.Context, client *http.Client, method, endpoint string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, SpotifyAPIBase+endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Spotify API error (%s): %s", resp.Status, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}
