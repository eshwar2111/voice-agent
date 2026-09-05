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

type openaiMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []openaiContentPart
}

type openaiContentPart struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL *openaiImageURL `json:"image_url,omitempty"`
}

type openaiImageURL struct {
	URL string `json:"url"`
}

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Temperature float32         `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type openaiStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

type OpenAICompatibleProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

func init() {
	// Every OpenAI-compatible service (OpenAI, OpenRouter, Groq, Together, and
	// local runtimes like Ollama / LM Studio) speaks the same /chat/completions
	// API — only the base URL differs. Register each name with its default base
	// URL so the user just picks a provider + key; a config base_url still wins.
	reg := func(name, defaultBase string) {
		Register(name, func(key, model, baseURL string) Provider {
			if strings.TrimSpace(baseURL) == "" {
				baseURL = defaultBase
			}
			return NewOpenAICompatible(key, model, baseURL)
		})
	}
	reg("openai", "https://api.openai.com/v1")
	reg("openrouter", "https://openrouter.ai/api/v1")
	reg("groq", "https://api.groq.com/openai/v1")
	reg("together", "https://api.together.xyz/v1")
	reg("ollama", "http://localhost:11434/v1")
	reg("lmstudio", "http://localhost:1234/v1")
	reg("local", "http://localhost:11434/v1")
	reg("custom", "") // caller MUST supply base_url
}

func NewOpenAICompatible(apiKey, model, baseURL string) *OpenAICompatibleProvider {
	if model == "" {
		model = "gpt-4o"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	// Make sure baseURL doesn't end with slash, we will append /chat/completions
	baseURL = strings.TrimSuffix(baseURL, "/")

	return &OpenAICompatibleProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client: &http.Client{
			Transport: newProviderTransport(),
		},
	}
}

func (p *OpenAICompatibleProvider) makeRequest(ctx context.Context, payload openaiRequest) (*http.Response, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := p.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	return p.client.Do(req)
}

func (p *OpenAICompatibleProvider) ClassifyAndPlan(ctx context.Context, transcript, toolSchemas, systemContext string) (ClassifyResponse, error) {
	prompt := buildClassifyPrompt(toolSchemas, systemContext)
	req := openaiRequest{
		Model: p.model,
		Messages: []openaiMessage{
			{Role: "system", Content: prompt},
			{Role: "user", Content: transcript},
		},
		Temperature: 0.1,
	}

	resp, err := p.makeRequest(ctx, req)
	if err != nil {
		return ClassifyResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ClassifyResponse{}, fmt.Errorf("OpenAI API error: %s", string(body))
	}

	var result openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ClassifyResponse{}, err
	}

	if len(result.Choices) == 0 {
		return ClassifyResponse{}, errors.New("no completion choices returned")
	}

	content := stripMarkdownCodeBlock(result.Choices[0].Message.Content)

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

func (p *OpenAICompatibleProvider) GenerateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error) {
	prompt := buildPlanningPrompt(req.ToolSchemas, req.SystemContext, true)

	var userParts []openaiContentPart
	userParts = append(userParts, openaiContentPart{Type: "text", Text: req.UserText})

	if len(req.Image) > 0 {
		base64Image := base64.StdEncoding.EncodeToString(req.Image)
		userParts = append(userParts, openaiContentPart{
			Type: "image_url",
			ImageURL: &openaiImageURL{
				URL: "data:image/jpeg;base64," + base64Image,
			},
		})
	}

	payload := openaiRequest{
		Model: p.model,
		Messages: []openaiMessage{
			{Role: "system", Content: prompt},
			{Role: "user", Content: userParts},
		},
		Temperature: 0.2,
	}

	resp, err := p.makeRequest(ctx, payload)
	if err != nil {
		return IntentResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return IntentResponse{}, fmt.Errorf("OpenAI API error: %s", string(body))
	}

	var result openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return IntentResponse{}, err
	}

	if len(result.Choices) == 0 {
		return IntentResponse{}, errors.New("no completion choices returned")
	}

	content := stripMarkdownCodeBlock(result.Choices[0].Message.Content)
	return IntentResponse{RawJSON: content}, nil
}

func (p *OpenAICompatibleProvider) StreamGenerateIntent(ctx context.Context, req IntentRequest, textDeltaChan chan<- string) (IntentResponse, error) {
	prompt := buildPlanningPrompt(req.ToolSchemas, req.SystemContext, true)

	var userParts []openaiContentPart
	userParts = append(userParts, openaiContentPart{Type: "text", Text: req.UserText})

	if len(req.Image) > 0 {
		base64Image := base64.StdEncoding.EncodeToString(req.Image)
		userParts = append(userParts, openaiContentPart{
			Type: "image_url",
			ImageURL: &openaiImageURL{
				URL: "data:image/jpeg;base64," + base64Image,
			},
		})
	}

	payload := openaiRequest{
		Model: p.model,
		Messages: []openaiMessage{
			{Role: "system", Content: prompt},
			{Role: "user", Content: userParts},
		},
		Temperature: 0.2,
		Stream:      true,
	}

	resp, err := p.makeRequest(ctx, payload)
	if err != nil {
		return IntentResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return IntentResponse{}, fmt.Errorf("OpenAI API error: %s", string(body))
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
		if lineStr == "" {
			continue
		}
		if !strings.HasPrefix(lineStr, "data: ") {
			continue
		}

		dataStr := strings.TrimPrefix(lineStr, "data: ")
		if dataStr == "[DONE]" {
			break
		}

		var streamResp openaiStreamResponse
		if err := json.Unmarshal([]byte(dataStr), &streamResp); err != nil {
			continue
		}

		if len(streamResp.Choices) > 0 {
			chunk := streamResp.Choices[0].Delta.Content
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

func (p *OpenAICompatibleProvider) Generate(ctx context.Context, prompt string, images [][]byte) (string, error) {
	var userParts []openaiContentPart
	userParts = append(userParts, openaiContentPart{Type: "text", Text: prompt})

	for _, img := range images {
		base64Image := base64.StdEncoding.EncodeToString(img)
		userParts = append(userParts, openaiContentPart{
			Type: "image_url",
			ImageURL: &openaiImageURL{
				URL: "data:image/jpeg;base64," + base64Image,
			},
		})
	}

	payload := openaiRequest{
		Model: p.model,
		Messages: []openaiMessage{
			{Role: "user", Content: userParts},
		},
		Temperature: 0.7,
	}

	resp, err := p.makeRequest(ctx, payload)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error: %s", string(body))
	}

	var result openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", errors.New("no completion choices returned")
	}

	return result.Choices[0].Message.Content, nil
}

// StreamGenerate emits the whole answer as one delta (the OpenAI path is not
// token-streamed for the answer surface yet; it still honors the contract).
func (p *OpenAICompatibleProvider) StreamGenerate(ctx context.Context, prompt string, images [][]byte, ch chan<- string) (string, error) {
	return streamViaGenerate(func() (string, error) { return p.Generate(ctx, prompt, images) }, ch)
}
