package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []anthropicContentPart
}

type anthropicContentPart struct {
	Type   string            `json:"type"`
	Text   string            `json:"text,omitempty"`
	Source *anthropicImageSrc `json:"source,omitempty"`
}

type anthropicImageSrc struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float32            `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type AnthropicProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

func init() {
	Register("anthropic", func(key, model, baseURL string) Provider {
		return NewAnthropic(key, model, baseURL)
	})
}

func NewAnthropic(apiKey, model, baseURL string) *AnthropicProvider {
	if model == "" {
		model = "claude-3-5-sonnet-20240620"
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	return &AnthropicProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Transport: newProviderTransport(),
		},
	}
}

func (p *AnthropicProvider) makeRequest(ctx context.Context, payload anthropicRequest) (*http.Response, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := p.baseURL + "/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	return p.client.Do(req)
}

func (p *AnthropicProvider) ClassifyAndPlan(ctx context.Context, transcript, toolSchemas, systemContext string) (ClassifyResponse, error) {
	prompt := buildClassifyPrompt(toolSchemas, systemContext)
	req := anthropicRequest{
		Model:     p.model,
		System:    prompt,
		Messages:  []anthropicMessage{{Role: "user", Content: transcript}},
		MaxTokens: 1024,
		Temperature: 0.1,
	}

	resp, err := p.makeRequest(ctx, req)
	if err != nil {
		return ClassifyResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ClassifyResponse{}, fmt.Errorf("Anthropic API error: %s", string(body))
	}

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ClassifyResponse{}, err
	}

	if len(result.Content) == 0 {
		return ClassifyResponse{}, errors.New("no content returned")
	}

	content := stripMarkdownCodeBlock(result.Content[0].Text)

	var probe struct {
		NeedsScreen bool `json:"needs_screen"`
	}
	if err := json.Unmarshal([]byte(content), &probe); err != nil {
		// Safe fallback: if we cannot tell, take the screen path.
		return ClassifyResponse{NeedsScreen: true}, nil
	}
	if probe.NeedsScreen {
		return ClassifyResponse{NeedsScreen: true}, nil
	}
	return ClassifyResponse{NeedsScreen: false, RawJSON: content}, nil
}

func (p *AnthropicProvider) GenerateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error) {
	prompt := buildPlanningPrompt(req.ToolSchemas, req.SystemContext, true)

	var userParts []anthropicContentPart
	if len(req.Image) > 0 {
		base64Image := base64.StdEncoding.EncodeToString(req.Image)
		userParts = append(userParts, anthropicContentPart{
			Type: "image",
			Source: &anthropicImageSrc{
				Type:      "base64",
				MediaType: "image/jpeg",
				Data:      base64Image,
			},
		})
	}
	userParts = append(userParts, anthropicContentPart{Type: "text", Text: req.UserText})

	payload := anthropicRequest{
		Model:     p.model,
		System:    prompt,
		Messages:  []anthropicMessage{{Role: "user", Content: userParts}},
		MaxTokens: 2048,
		Temperature: 0.2,
	}

	resp, err := p.makeRequest(ctx, payload)
	if err != nil {
		return IntentResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return IntentResponse{}, fmt.Errorf("Anthropic API error: %s", string(body))
	}

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return IntentResponse{}, err
	}

	if len(result.Content) == 0 {
		return IntentResponse{}, errors.New("no content returned")
	}

	content := stripMarkdownCodeBlock(result.Content[0].Text)
	return IntentResponse{RawJSON: content}, nil
}

func (p *AnthropicProvider) StreamGenerateIntent(ctx context.Context, req IntentRequest, textDeltaChan chan<- string) (IntentResponse, error) {
	prompt := buildPlanningPrompt(req.ToolSchemas, req.SystemContext, true)

	var userParts []anthropicContentPart
	if len(req.Image) > 0 {
		base64Image := base64.StdEncoding.EncodeToString(req.Image)
		userParts = append(userParts, anthropicContentPart{
			Type: "image",
			Source: &anthropicImageSrc{
				Type:      "base64",
				MediaType: "image/jpeg",
				Data:      base64Image,
			},
		})
	}
	userParts = append(userParts, anthropicContentPart{Type: "text", Text: req.UserText})

	payload := anthropicRequest{
		Model:     p.model,
		System:    prompt,
		Messages:  []anthropicMessage{{Role: "user", Content: userParts}},
		MaxTokens: 2048,
		Temperature: 0.2,
		Stream:    true,
	}

	resp, err := p.makeRequest(ctx, payload)
	if err != nil {
		return IntentResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return IntentResponse{}, fmt.Errorf("Anthropic API error: %s", string(body))
	}

	var fullContent strings.Builder
	reader := bufio.NewReader(resp.Body)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return IntentResponse{}, err
		}

		lineStr := strings.TrimSpace(string(line))
		if !strings.HasPrefix(lineStr, "data: ") {
			continue
		}

		dataStr := strings.TrimPrefix(lineStr, "data: ")
		
		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(dataStr), &event); err != nil {
			continue
		}

		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			chunk := event.Delta.Text
			if chunk != "" {
				fullContent.WriteString(chunk)
				if textDeltaChan != nil {
					textDeltaChan <- chunk
				}
			}
		}
	}

	content := stripMarkdownCodeBlock(fullContent.String())
	return IntentResponse{RawJSON: content}, nil
}

func (p *AnthropicProvider) Generate(ctx context.Context, prompt string, images [][]byte) (string, error) {
	var userParts []anthropicContentPart
	
	for _, img := range images {
		base64Image := base64.StdEncoding.EncodeToString(img)
		userParts = append(userParts, anthropicContentPart{
			Type: "image",
			Source: &anthropicImageSrc{
				Type:      "base64",
				MediaType: "image/jpeg",
				Data:      base64Image,
			},
		})
	}
	userParts = append(userParts, anthropicContentPart{Type: "text", Text: prompt})

	payload := anthropicRequest{
		Model:     p.model,
		Messages:  []anthropicMessage{{Role: "user", Content: userParts}},
		MaxTokens: 1024,
		Temperature: 0.7,
	}

	resp, err := p.makeRequest(ctx, payload)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Anthropic API error: %s", string(body))
	}

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Content) == 0 {
		return "", errors.New("no content returned")
	}

	return result.Content[0].Text, nil
}
