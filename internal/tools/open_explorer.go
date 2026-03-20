package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type OpenExplorerTool struct{}

func (o *OpenExplorerTool) Name() string {
	return "open_explorer"
}

func (o *OpenExplorerTool) Description() string {
	return "Opens the Windows File Explorer at a specific path."
}

func (o *OpenExplorerTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"path": { "type": "string", "description": "The absolute or relative directory path to open." }
		},
		"required": ["path"]
	}`
}

func (o *OpenExplorerTool) RequiresConfirmation() bool {
	return false
}

type OpenExplorerArgs struct {
	Path string `json:"path"`
}

func (o *OpenExplorerTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params OpenExplorerArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	path := params.Path
	if strings.TrimSpace(path) == "" {
		return "", errors.New("missing path parameter")
	}

	// Verify path exists
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "", errors.New("the specified path does not exist or is not a directory")
	}

	lowerPath := strings.ToLower(path)
	for _, restricted := range RestrictedPaths {
		if strings.Contains(lowerPath, restricted) {
			return "", errors.New("action blocked: path contains restricted directory")
		}
	}

	fmt.Printf("Opening explorer to: %s\n", path)
	cmd := exec.Command("explorer", path)
	err := cmd.Start()
	if err != nil {
		return "", err
	}
	return "Explorer opened", nil
}
