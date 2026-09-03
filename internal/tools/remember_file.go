package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/internal/search"
)

// RememberFileTool pins an explicit alias to a file, so a phrase like
// "my latest resume" resolves instantly to that path thereafter. Use it when the
// user says something like "remember this as my resume" or "this is my budget".
type RememberFileTool struct{}

func (r *RememberFileTool) Name() string {
	return "remember_file"
}

func (r *RememberFileTool) Description() string {
	return "Pins an explicit name/alias to a file so the user can refer to it by that name later (e.g. remember \"latest resume\" -> Resume_2026.pdf). Resolves the file from a query, then stores the alias."
}

func (r *RememberFileTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"key":   { "type": "string", "description": "The alias to remember the file by, e.g. \"latest resume\" or \"my budget\"." },
			"query": { "type": "string", "description": "The file to pin, as the user named it, e.g. \"Resume 2026\" or \"the spreadsheet I just opened\"." }
		},
		"required": ["key", "query"]
	}`
}

func (r *RememberFileTool) RequiresConfirmation() bool {
	return false
}

type RememberFileArgs struct {
	Key   string `json:"key"`
	Query string `json:"query"`
}

func (r *RememberFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params RememberFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	key := strings.TrimSpace(params.Key)
	query := strings.TrimSpace(params.Query)
	if key == "" || query == "" {
		return "", fmt.Errorf("both key and query are required")
	}

	path, ok := search.Resolve(query)
	if !ok {
		if recs := search.SearchFiles(query); len(recs) > 0 {
			path = recs[0].Path
		} else {
			return "", fmt.Errorf("could not find a file matching %q to remember", query)
		}
	}

	if err := search.Remember(key, path); err != nil {
		return "", fmt.Errorf("failed to remember file: %w", err)
	}

	return fmt.Sprintf("Remembered %q as %s", key, path), nil
}
