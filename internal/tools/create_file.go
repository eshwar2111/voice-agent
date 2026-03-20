package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var RestrictedPaths = []string{
	"system32",
	"windows",
	"program files",
}

type CreateFileTool struct{}

func (c *CreateFileTool) Name() string {
	return "create_file"
}

func (c *CreateFileTool) Description() string {
	return "Creates an empty file at the specified path"
}

func (c *CreateFileTool) Parameters() string {
	return `{"filename": "string (required - file name or absolute path)"}` // Note: The implementation also reads 'path' from input, but 'filename' handles full paths. Wait, the tool has 'path' and 'filename' separately.
}

func (c *CreateFileTool) RequiresConfirmation() bool {
	return true // Write actions might require confirmation
}

type CreateFileArgs struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	FileName string `json:"file_name"` // Alias
}

func (c *CreateFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params CreateFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	path := params.Path
	if strings.TrimSpace(path) == "" {
		// Try to fallback to current directory
		cwd, err := os.Getwd()
		if err != nil {
			path = "."
		} else {
			path = cwd
		}
	} else {
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
	}

	filename := params.Filename
	if filename == "" {
		filename = params.FileName // Attempt alias
	}
	if filename == "" {
		filename = "new_file.txt"
	}
	fullPath := filepath.Join(path, filename)

	// Create empty file
	err := os.WriteFile(fullPath, []byte(""), 0644)
	if err != nil {
		return "", err
	}
	fmt.Printf("File created successfully at: %s\n", fullPath)

	// Open explorer to that file
	cmd := exec.Command("explorer", `/select,`, fullPath)
	err = cmd.Start()
	if err != nil {
		return "", err
	}
	return "File created successfully", nil
}
