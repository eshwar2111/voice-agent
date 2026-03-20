package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WriteFileTool struct{}

func (w *WriteFileTool) Name() string {
	return "write_file"
}

func (w *WriteFileTool) Description() string {
	return "Writes or overwrites text content to a local file"
}

func (w *WriteFileTool) Parameters() string {
	return `{"path": "string (required - absolute Windows path, e.g. 'C:/Users/Eshwar/Desktop/notes.txt')", "content": "string (required - use '{PREVIOUS_OUTPUT}' to insert the previous tool's result)"}`
}

func (w *WriteFileTool) RequiresConfirmation() bool {
	return false // confirmation is already handled at the plan level
}

type WriteFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (w *WriteFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params WriteFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	path := params.Path
	content := params.Content

	if strings.TrimSpace(path) == "" {
		return "", errors.New("missing path parameter")
	}

	lowerPath := strings.ToLower(path)
	for _, restricted := range RestrictedPaths {
		if strings.Contains(lowerPath, restricted) {
			return "", errors.New("action blocked: path contains restricted directory")
		}
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path), nil
}
