package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type MoveFileTool struct{}

func (t *MoveFileTool) Name() string {
	return "move_file"
}

func (t *MoveFileTool) Description() string {
	return "Renames or moves a file/directory from source to destination"
}

func (t *MoveFileTool) Parameters() string {
	return `{"src": "string (required - source path)", "dst": "string (required - destination path)"}`
}

func (t *MoveFileTool) RequiresConfirmation() bool {
	return false
}

type MoveFileArgs struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

func (t *MoveFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params MoveFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	src := params.Src
	dst := params.Dst

	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return "", fmt.Errorf("missing src or dst parameter")
	}

	for _, path := range []string{src, dst} {
		lowerPath := strings.ToLower(path)
		for _, restricted := range RestrictedPaths {
			if strings.Contains(lowerPath, restricted) {
				return "", fmt.Errorf("action blocked: path contains restricted directory")
			}
		}
	}

	// Ensure destination directory exists
	destDir := filepath.Dir(dst)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	err := os.Rename(src, dst)
	if err != nil {
		return "", fmt.Errorf("failed to move: %w", err)
	}

	return fmt.Sprintf("Successfully moved %s to %s", src, dst), nil
}
