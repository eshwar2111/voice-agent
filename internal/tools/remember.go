package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/internal/memory"
)

// RememberTool stores a user-provided fact or event into persistent memory.
type RememberTool struct {
	Store *memory.Store
}

func (t *RememberTool) Name() string               { return "remember" }
func (t *RememberTool) RequiresConfirmation() bool { return false }
func (t *RememberTool) Description() string {
	return "Stores a piece of information (fact, preference, or event) into persistent memory for future recall."
}
func (t *RememberTool) Parameters() string {
	return `{"fact": {"type": "string"}, "type": {"type": "string", "description": "episodic|semantic|task"}, "importance": {"type": "string", "description": "low|medium|high"}}`
}

type RememberArgs struct {
	Fact       string `json:"fact"`
	Type       string `json:"type"`
	Importance string `json:"importance"`
}

func (t *RememberTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params RememberArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	fact := params.Fact
	if fact == "" {
		return "", fmt.Errorf("remember: 'fact' parameter is required")
	}

	memType := memory.TypeSemantic
	switch params.Type {
	case "episodic":
		memType = memory.TypeEpisodic
	case "task":
		memType = memory.TypeTask
	}

	importance := memory.ImportanceMedium
	switch params.Importance {
	case "low":
		importance = memory.ImportanceLow
	case "high":
		importance = memory.ImportanceHigh
	}

	id, err := t.Store.Save(memType, fact, importance)
	if err != nil {
		return "", fmt.Errorf("remember: failed to save: %w", err)
	}
	return fmt.Sprintf("Remembered: %s (id=%s)", fact, id), nil
}
