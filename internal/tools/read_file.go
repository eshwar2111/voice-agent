package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
)

type ReadFileTool struct{}

func (r *ReadFileTool) Name() string {
	return "read_file"
}

func (r *ReadFileTool) Description() string {
	return "Reads the contents of a local text file"
}

func (r *ReadFileTool) Parameters() string {
	return `{"path": "string (required - exact absolute path or relative path to the file to read)"}`
}

func (r *ReadFileTool) RequiresConfirmation() bool {
	return false
}

type ReadFileArgs struct {
	Path string `json:"path"`
}

func (t *ReadFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params ReadFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	path := params.Path
	if strings.TrimSpace(path) == "" {
		return "", errors.New("missing path parameter")
	}

	// For safety, only allow reading obvious text extensions to prevent binary dumping
	ext := strings.ToLower(filepath.Ext(path))
	safeExts := map[string]bool{
		".txt": true, ".md": true, ".log": true, ".yaml": true, ".yml": true,
		".json": true, ".xml": true, ".csv": true, ".go": true, ".py": true,
		".js": true, ".ts": true, ".html": true, ".css": true,
	}

	if ext != "" && !safeExts[ext] {
		return "", errors.New("unsafe or binary file extension blocked")
	}

	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}

	// Limit to reasonable size to prevent context overflow (roughly 50KB = ~10k tokens)
	content := string(bytes)
	if len(content) > 50000 {
		content = content[:50000] + "\n...[TRUNCATED FOR LENGTH]"
	}

	return content, nil
}
