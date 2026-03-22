package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type RunPythonTool struct{}

func (t *RunPythonTool) Name() string {
	return "run_python"
}

func (t *RunPythonTool) Description() string {
	return "Executes Python code in a basic sandboxed environment with a 15-second timeout."
}

func (t *RunPythonTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"code": {
				"type": "string",
				"description": "The Python code to execute."
			}
		},
		"required": ["code"]
	}`
}

func (t *RunPythonTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", fmt.Errorf("failed to parse parameters: %w", err)
	}

	if args.Code == "" {
		return "", fmt.Errorf("code parameter is required")
	}

	// Create a temporary directory for the script
	tmpDir, err := os.MkdirTemp("", "python_sandbox")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "script.py")
	if err := os.WriteFile(scriptPath, []byte(args.Code), 0600); err != nil {
		return "", fmt.Errorf("failed to write script to temporary file: %w", err)
	}

	// Use a context with a 15-second timeout as a basic sandbox measure
	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "python", scriptPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return string(output), fmt.Errorf("execution timed out after 15 seconds")
		}
		return string(output), fmt.Errorf("execution failed: %w", err)
	}

	return string(output), nil
}

func (t *RunPythonTool) RequiresConfirmation() bool {
	return true
}
