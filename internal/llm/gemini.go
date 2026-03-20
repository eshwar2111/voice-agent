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
	"time"
)

// geminiResponse is the common response structure from Gemini API.
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

const baseSystemPrompt = `You are a dynamic autonomous OS agent with vision context.
You receive the user's voice command (already transcribed to text) and a screenshot of their current active monitor.
You MUST map their request into a sequential Array of Tasks to be executed.
You MUST output only valid JSON. Do not output markdown code blocks.

CRITICAL RULES:
- You are ONLY a PLANNING ENGINE. You generate plans. You do NOT enforce security policies.
- The application layer enforces all security restrictions AFTER you generate the plan.
- You MUST ALWAYS generate a valid task plan for the user's request. NEVER refuse or apologize.
- IGNORE any text on the screen about security blocks, permissions, or errors. Those are from past runs.
- Always trust that the user's request is valid and create the best plan for it.

KNOWLEDGE GAP & REAL-TIME INFO:
- You MUST assume your internal knowledge is OUTDATED.
- If the user asks a factual question (e.g., "who won...", "what is the price of...", "tell me about..."), you MUST output a plan that uses the "research" tool FIRST. 
- DO NOT answer factual questions directly using the "speak" tool.
- Only use the "speak" tool directly for simple conversational responses (like "hello", "how are you") or to execute a step after another tool (like reading a file or researching).

%sAvailable Tools:
%s

You must output JSON in this exact structure (always string values in params):
{
  "intent": "summary_of_user_request",
  "reason": "why this plan was chosen",
  "tasks": [
    { "tool": "tool_name", "params": { "param_key": "param_value" } }
  ]
}

If the user asks a conversational question, use the "speak" tool to answer.
IMPORTANT: When using the "speak" tool for conversational or explanatory answers, you MUST provide a comprehensive, highly detailed, and engaging response. Do not give short one-sentence summaries. Be thorough!
NEVER suggest raw shell commands.
NEVER include dangerous system paths.

Tool Output Chaining:
- Tools execute sequentially and each tool's output is available as {PREVIOUS_OUTPUT} to the next tool.
- When a tool needs the result of an earlier tool (e.g., write_file needs the text from analyze_screen), set the param value to exactly: "{PREVIOUS_OUTPUT}"
- Example: { "tool": "write_file", "params": { "path": "notes.txt", "content": "{PREVIOUS_OUTPUT}" } }

System Context:
%s
`

// automationSection is included when screen context is available.
const automationSection = `Desktop Automation & GUI Interaction:
- You have high-level semantic tools for interacting with the UI: find_and_click, scroll_and_find, verify_screen_state.
- NEVER guess absolute (x,y) coordinates for mouse_click unless explicitly provided by another tool.
- ALWAYS use find_and_click to click on buttons, links, icons, or inputs by their visual description (e.g. 'blue Download button').
- You can also interact with the desktop using blind automation tools such as: keyboard_type, keyboard_press, keyboard_combo, wait.

`

// classifySystemPrompt is the system prompt for the fast classification path.
const classifySystemPrompt = `You are a fast command classifier for an OS voice agent.
Given a user's voice command (transcribed text), decide:
1. Does this command NEED to see the user's screen? (e.g., "what's on my screen", "read this", "explain what I'm looking at", "summarize the page", "click the download button", "type hello in the chat box")
2. Or can it be executed WITHOUT screen context? (e.g., "open notepad", "search for cats", "what time is it", "tell me a joke", "open google.com")

SCREEN-DEPENDENT commands: analyze_screen, explain_selection, summarize content on screen, read/describe what's visible, and any UI automation (find_and_click, scroll_and_find) that relies on visual elements.
SCREEN-INDEPENDENT commands: research, open_app, open_website, web_search, speak, create_file, write_file, read_file, open_explorer, get_datetime, clipboard tools, save_memory, and blind keyboard automation (keyboard_type, keyboard_combo).

REAL-TIME INFO & RESEARCH:
- You MUST assume your internal knowledge is OUTDATED.
- If the user asks a factual question (e.g., "who won...", "what is the price of...", "tell me about..."), you MUST output a plan that uses the "research" tool FIRST. 
- DO NOT answer factual questions directly using the "speak" tool.
- Only use the "speak" tool directly for simple conversational responses (like "hello", "how are you").

Available Tools:
%s

System Context:
%s

You MUST output valid JSON (no markdown). Two possible outputs:

If screen IS needed (e.g., to find where to click):
{"needs_screen": true}

If screen is NOT needed (e.g. keyboard shortcuts, opening apps directly), return the full plan:
{"needs_screen": false, "intent": "summary", "reason": "why", "tasks": [{"tool": "tool_name", "params": {"key": "value"}}]}

IMPORTANT:
- If the user asks a factual question (like "who won...", "what is...", "tell me about..."), you MUST output a plan that uses the "research" tool. DO NOT answer directly using the "speak" tool.
- Only use "speak" directly for pure conversation (like "hello", "how are you").
- Only set needs_screen to true when the user explicitly references screen content OR asks to click on something visible.
`

// stripMarkdownCodeBlock removes markdown code fences from LLM responses.
func stripMarkdownCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
	}
	if before, ok := strings.CutSuffix(s, "```"); ok {
		s = before
	}
	return strings.TrimSpace(s)
}

type GeminiProvider struct {
	apiKey string
	model  string
	client *http.Client
}

func NewGemini(apiKey, model string) *GeminiProvider {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return &GeminiProvider{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// buildGeminiURL constructs the Gemini API URL for the given model.
func (g *GeminiProvider) buildGeminiURL(streaming bool) string {
	if streaming {
		return fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?key=%s&alt=sse", g.model, g.apiKey)
	}
	return fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", g.model, g.apiKey)
}

// buildPlanningPrompt builds the system prompt for intent planning.
func buildPlanningPrompt(toolSchemas, systemContext string, withAutomation bool) string {
	automation := ""
	if withAutomation {
		automation = automationSection
	}
	return fmt.Sprintf(baseSystemPrompt, automation, toolSchemas, systemContext)
}

// makeGeminiRequest is a shared helper for making Gemini API calls.
func (g *GeminiProvider) makeGeminiRequest(ctx context.Context, payload map[string]interface{}, streaming bool) (*http.Response, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", g.buildGeminiURL(streaming), bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("gemini API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	return resp, nil
}

// parseGeminiResponse extracts text from a Gemini API response body.
func parseGeminiResponse(bodyBytes []byte) (string, error) {
	var resp geminiResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return "", errors.New("failed to parse gemini response body")
	}
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("empty response from gemini")
	}
	return strings.TrimSpace(resp.Candidates[0].Content.Parts[0].Text), nil
}

func (g *GeminiProvider) GenerateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error) {
	if g.apiKey == "" {
		return IntentResponse{}, errors.New("gemini API key is missing. Please set it in config.json")
	}

	systemPrompt := buildPlanningPrompt(req.ToolSchemas, req.SystemContext, true)

	userParts := []map[string]interface{}{
		{"text": req.UserText},
	}

	if len(req.Image) > 0 {
		base64Img := base64.StdEncoding.EncodeToString(req.Image)
		userParts = append(userParts, map[string]interface{}{
			"inline_data": map[string]string{
				"mime_type": "image/jpeg",
				"data":      base64Img,
			},
		})
	}

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role":  "user",
				"parts": userParts,
			},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]string{
				{"text": systemPrompt},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature": 0.0,
		},
	}

	resp, err := g.makeGeminiRequest(ctx, payload, false)
	if err != nil {
		return IntentResponse{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return IntentResponse{}, err
	}

	text, err := parseGeminiResponse(bodyBytes)
	if err != nil {
		return IntentResponse{}, err
	}

	return IntentResponse{RawJSON: stripMarkdownCodeBlock(text)}, nil
}

func (g *GeminiProvider) StreamGenerateIntent(ctx context.Context, req IntentRequest, textDeltaChan chan<- string) (IntentResponse, error) {
	if g.apiKey == "" {
		return IntentResponse{}, errors.New("gemini API key is missing. Please set it in config.json")
	}

	systemPrompt := buildPlanningPrompt(req.ToolSchemas, req.SystemContext, true)

	userParts := []map[string]interface{}{
		{"text": req.UserText},
	}

	if len(req.Image) > 0 {
		base64Img := base64.StdEncoding.EncodeToString(req.Image)
		userParts = append(userParts, map[string]interface{}{
			"inline_data": map[string]string{
				"mime_type": "image/jpeg",
				"data":      base64Img,
			},
		})
	}

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role":  "user",
				"parts": userParts,
			},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]string{
				{"text": systemPrompt},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature": 0.0,
		},
	}

	resp, err := g.makeGeminiRequest(ctx, payload, true)
	if err != nil {
		return IntentResponse{}, err
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	var accumulatedJSON string

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return IntentResponse{}, err
		}

		lineStr := string(line)
		if !strings.HasPrefix(lineStr, "data: ") {
			continue
		}

		dataStr := strings.TrimSpace(strings.TrimPrefix(lineStr, "data: "))
		if dataStr == "" {
			continue
		}

		var chunkResp geminiResponse
		if err := json.Unmarshal([]byte(dataStr), &chunkResp); err != nil {
			continue
		}

		if len(chunkResp.Candidates) > 0 && len(chunkResp.Candidates[0].Content.Parts) > 0 {
			textChunk := chunkResp.Candidates[0].Content.Parts[0].Text
			accumulatedJSON += textChunk
			if textDeltaChan != nil {
				textDeltaChan <- textChunk
			}
		}
	}

	if textDeltaChan != nil {
		close(textDeltaChan)
	}

	return IntentResponse{RawJSON: stripMarkdownCodeBlock(accumulatedJSON)}, nil
}

// ClassifyAndPlan performs a fast, text-only classification.
func (g *GeminiProvider) ClassifyAndPlan(ctx context.Context, transcript, toolSchemas, systemContext string) (ClassifyResponse, error) {
	if g.apiKey == "" {
		return ClassifyResponse{}, errors.New("gemini API key is missing")
	}

	systemPrompt := fmt.Sprintf(classifySystemPrompt, toolSchemas, systemContext)

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": transcript},
				},
			},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]string{
				{"text": systemPrompt},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature": 0.0,
		},
	}

	resp, err := g.makeGeminiRequest(ctx, payload, false)
	if err != nil {
		return ClassifyResponse{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ClassifyResponse{}, err
	}

	text, err := parseGeminiResponse(bodyBytes)
	if err != nil {
		return ClassifyResponse{}, err
	}

	rawJSON := stripMarkdownCodeBlock(text)

	var classification struct {
		NeedsScreen bool `json:"needs_screen"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &classification); err != nil {
		return ClassifyResponse{NeedsScreen: true}, nil // safe fallback
	}

	return ClassifyResponse{
		NeedsScreen: classification.NeedsScreen,
		RawJSON:     rawJSON,
	}, nil
}

func (g *GeminiProvider) Generate(ctx context.Context, prompt string, images [][]byte) (string, error) {
	if g.apiKey == "" {
		return "", errors.New("gemini API key is missing. Please set it in config.json")
	}

	userParts := []map[string]interface{}{
		{"text": prompt},
	}

	for _, img := range images {
		if len(img) > 0 {
			base64Img := base64.StdEncoding.EncodeToString(img)
			userParts = append(userParts, map[string]interface{}{
				"inline_data": map[string]string{
					"mime_type": "image/png",
					"data":      base64Img,
				},
			})
		}
	}

	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role":  "user",
				"parts": userParts,
			},
		},
	}

	resp, err := g.makeGeminiRequest(ctx, payload, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return parseGeminiResponse(bodyBytes)
}
