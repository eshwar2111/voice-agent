package tools

import (
	"context"
	"encoding/json"
	"fmt"

	context_api "github.com/yourname/voice-agent/internal/context"
	"github.com/yourname/voice-agent/internal/llm"
)

type SummarizeClipboardTool struct {
	Provider llm.Provider
}

func (t *SummarizeClipboardTool) Name() string {
	return "summarize_clipboard"
}

func (t *SummarizeClipboardTool) Description() string {
	return "Summarizes the current content of the user's clipboard"
}

func (t *SummarizeClipboardTool) Parameters() string {
	return `{}`
}

func (t *SummarizeClipboardTool) RequiresConfirmation() bool {
	return false
}

func (t *SummarizeClipboardTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	text, err := context_api.GetClipboardText()
	if err != nil {
		return "", err
	}
	if len(text) == 0 {
		return "Clipboard is empty", nil
	}

	prompt := fmt.Sprintf("Summarize the following text:\n\n%s", text)
	response, err := t.Provider.Generate(ctx, prompt, nil)
	return response, err
}

type RewriteClipboardTool struct {
	Provider llm.Provider
}

func (t *RewriteClipboardTool) Name() string {
	return "rewrite_clipboard"
}

func (t *RewriteClipboardTool) Description() string {
	return "Rewrites the text in the clipboard in a specified style"
}

func (t *RewriteClipboardTool) Parameters() string {
	return `{"style": "string (optional - e.g. 'professional', 'casual', 'academic', defaults to 'professional')"}`
}

func (t *RewriteClipboardTool) RequiresConfirmation() bool {
	return false
}

type RewriteClipboardArgs struct {
	Style string `json:"style"`
}

func (t *RewriteClipboardTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params RewriteClipboardArgs
	if len(rawParams) > 0 && string(rawParams) != "null" && string(rawParams) != "{}" {
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return "", fmt.Errorf("invalid parameters: %w", err)
		}
	}

	style := params.Style
	if style == "" {
		style = "professional"
	}

	text, err := context_api.GetClipboardText()
	if err != nil {
		return "", err
	}
	if len(text) == 0 {
		return "Clipboard is empty", nil
	}

	prompt := fmt.Sprintf("Rewrite the following text in a %s tone:\n\n%s", style, text)
	response, err := t.Provider.Generate(ctx, prompt, nil)
	return response, err
}
