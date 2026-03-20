package tools

import (
	"context"
	"encoding/json"
	"fmt"

	context_api "github.com/yourname/voice-agent/internal/context"
	"github.com/yourname/voice-agent/internal/llm"
)

type ExplainSelectionTool struct {
	Provider llm.Provider
}

func (t *ExplainSelectionTool) Name() string {
	return "explain_selection"
}

func (t *ExplainSelectionTool) Description() string {
	return "Explains the text currently highlighted by the user"
}

func (t *ExplainSelectionTool) Parameters() string {
	return `{}`
}

func (t *ExplainSelectionTool) RequiresConfirmation() bool {
	return false
}

func (t *ExplainSelectionTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	text, err := context_api.CopySelectedText()
	if err != nil {
		return "", err
	}
	if len(text) == 0 {
		return "No text selected", nil
	}

	prompt := fmt.Sprintf("Explain the following text clearly:\n\n%s", text)
	response, err := t.Provider.Generate(ctx, prompt, nil)
	return response, err
}
