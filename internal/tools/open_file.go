package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type OpenFileTool struct{}

func (o *OpenFileTool) Name() string {
	return "open_file"
}

func (o *OpenFileTool) Description() string {
	return "Opens a file using its default application"
}

func (o *OpenFileTool) Parameters() string {
	return `{"file_path": "string (required, absolute path to the file)"}`
}

func (o *OpenFileTool) RequiresConfirmation() bool {
	return false
}

type OpenFileArgs struct {
	FilePath string `json:"file_path"`
}

func (o *OpenFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params OpenFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	if params.FilePath == "" {
		return "", fmt.Errorf("missing file_path parameter")
	}

	cmd := exec.Command("cmd.exe", "/c", "start", `""`, params.FilePath)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}

	return fmt.Sprintf("Successfully opened file: %s", params.FilePath), nil
}
