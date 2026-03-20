package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type ListFilesTool struct{}

func (t *ListFilesTool) Name() string {
	return "list_files"
}

func (t *ListFilesTool) Description() string {
	return "Lists files and directories in a specified path"
}

func (t *ListFilesTool) Parameters() string {
	return `{"path": "string (required - absolute Windows path)"}`
}

func (t *ListFilesTool) RequiresConfirmation() bool {
	return false
}

type ListFilesArgs struct {
	Path string `json:"path"`
}

func (t *ListFilesTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params ListFilesArgs
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

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}

	var result []string
	for _, entry := range entries {
		info, _ := entry.Info()
		typeStr := "File"
		if entry.IsDir() {
			typeStr = "Dir "
		}
		result = append(result, fmt.Sprintf("[%s] %s (%d bytes)", typeStr, entry.Name(), info.Size()))
	}

	if len(result) == 0 {
		return "Directory is empty", nil
	}

	return strings.Join(result, "\n"), nil
}
