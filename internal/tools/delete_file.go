package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type DeleteFileTool struct{}

func (t *DeleteFileTool) Name() string {
	return "delete_file"
}

func (t *DeleteFileTool) Description() string {
	return "Permanently deletes a file or directory at the specified path"
}

func (t *DeleteFileTool) Parameters() string {
	return `{"path": "string (required - absolute Windows path)"}`
}

func (t *DeleteFileTool) RequiresConfirmation() bool {
	return true
}

type DeleteFileArgs struct {
	Path string `json:"path"`
}

func (t *DeleteFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params DeleteFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	path := params.Path
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("missing path parameter")
	}

	lowerPath := strings.ToLower(path)
	for _, restricted := range RestrictedPaths {
		if strings.Contains(lowerPath, restricted) {
			return "", fmt.Errorf("action blocked: path contains restricted directory")
		}
	}

	err := os.RemoveAll(path)
	if err != nil {
		return "", fmt.Errorf("failed to delete: %w", err)
	}

	return fmt.Sprintf("Successfully deleted %s", path), nil
}
