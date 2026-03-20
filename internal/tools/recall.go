package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/internal/memory"
)

// RecallTool performs a semantic (FTS5) search over the persistent memory store.
type RecallTool struct {
	Retriever *memory.Retriever
}

func (t *RecallTool) Name() string               { return "recall" }
func (t *RecallTool) RequiresConfirmation() bool { return false }
func (t *RecallTool) Description() string {
	return "Searches persistent memory for stored facts, preferences, and past events related to the query."
}
func (t *RecallTool) Parameters() string {
	return `{"query": {"type": "string"}, "limit": {"type": "string", "description": "Max results, default 5"}}`
}

type RecallArgs struct {
	Query string `json:"query"`
	Limit string `json:"limit"`
}

func (t *RecallTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params RecallArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	query := params.Query
	if query == "" {
		return "", fmt.Errorf("recall: 'query' parameter is required")
	}

	limit := 5
	if params.Limit != "" {
		fmt.Sscanf(params.Limit, "%d", &limit)
	}

	results, err := t.Retriever.Search(query, limit)
	if err != nil {
		return "", fmt.Errorf("recall: search failed: %w", err)
	}

	if len(results) == 0 {
		return "No relevant memories found for: " + query, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d memories:\n", len(results)))
	for i, m := range results {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, m.Type, m.Content))
	}
	return sb.String(), nil
}
