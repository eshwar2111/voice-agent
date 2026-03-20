package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/internal/memory"
)

// ListMemoriesTool returns recent memories — useful for debugging and self-inspection.
type ListMemoriesTool struct {
	Store *memory.Store
}

func (t *ListMemoriesTool) Name() string               { return "list_memories" }
func (t *ListMemoriesTool) RequiresConfirmation() bool { return false }
func (t *ListMemoriesTool) Description() string {
	return "Lists the most recent entries stored in persistent memory."
}
func (t *ListMemoriesTool) Parameters() string {
	return `{"limit": {"type": "string", "description": "How many recent memories to show, default 10"}}`
}

type ListMemoriesArgs struct {
	Limit string `json:"limit"`
}

func (t *ListMemoriesTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params ListMemoriesArgs
	if len(rawParams) > 0 && string(rawParams) != "null" && string(rawParams) != "{}" {
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return "", fmt.Errorf("invalid parameters: %w", err)
		}
	}

	limit := 10
	if params.Limit != "" {
		fmt.Sscanf(params.Limit, "%d", &limit)
	}

	memories, err := t.Store.List(limit)
	if err != nil {
		return "", fmt.Errorf("list_memories: %w", err)
	}

	if len(memories) == 0 {
		return "Memory store is empty.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Last %d memories:\n", len(memories)))
	for i, m := range memories {
		sb.WriteString(fmt.Sprintf("%d. [%s | imp=%d] %s  (%s)\n",
			i+1, m.Type, m.Importance, m.Content, m.Timestamp.Format("2006-01-02 15:04")))
	}
	return sb.String(), nil
}
