package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/yourname/voice-agent/internal/search"
)

// FindFileTool resolves a spoken/typed file name or description to the best
// matching path from the persistent file index, opens it, and records the open
// so it ranks higher next time.
type FindFileTool struct{}

func (f *FindFileTool) Name() string {
	return "find_file"
}

func (f *FindFileTool) Description() string {
	return "Finds and opens a file by a name or description the user said (e.g. \"my latest resume\", \"the budget spreadsheet\"). Uses the prepared file index — no disk walk."
}

func (f *FindFileTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": { "type": "string", "description": "The file the user is asking for, as they said it, e.g. \"latest resume\" or \"voice agent notes\"." }
		},
		"required": ["query"]
	}`
}

func (f *FindFileTool) RequiresConfirmation() bool {
	return false
}

type FindFileArgs struct {
	Query string `json:"query"`
}

func (f *FindFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params FindFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return "", fmt.Errorf("missing query parameter")
	}

	path, ok := search.Resolve(query)
	if !ok {
		// Fall back to the ranked list's best hit if Resolve was below threshold.
		if recs := search.SearchFiles(query); len(recs) > 0 {
			path = recs[0].Path
		} else {
			return "", fmt.Errorf("could not find a file matching %q", query)
		}
	}

	cmd := exec.Command("cmd.exe", "/c", "start", `""`, path)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}

	// Usage learning: this open raises the file's rank for future resolves.
	search.RecordOpen(path)

	return fmt.Sprintf("Found and opened: %s", path), nil
}
