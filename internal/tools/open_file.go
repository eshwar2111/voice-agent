package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/yourname/voice-agent/internal/search"
)

type OpenFileTool struct{}

func (o *OpenFileTool) Name() string {
	return "open_file"
}

func (o *OpenFileTool) Description() string {
	return "Opens a file using its default application"
}

func (o *OpenFileTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"file_path": { "type": "string", "description": "Optional. An absolute path, if you already know it." },
			"query":     { "type": "string", "description": "Optional. The file NAME the user said, e.g. \"notes\" for \"open my notes file\". Use this whenever the user names a file but you do not know its path." }
		},
		"required": []
	}`
}

func (o *OpenFileTool) RequiresConfirmation() bool {
	return false
}

type OpenFileArgs struct {
	FilePath string `json:"file_path"`
	Query    string `json:"query"`
}

func (o *OpenFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params OpenFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	// Resolve folder aliases ("downloads/x.txt", "~/x") to real paths; a bare
	// name to search (not a known folder) is returned unchanged for resolveFile.
	path := resolveUserPath(strings.TrimSpace(params.FilePath))

	// Same trap open_explorer had: a spoken file name ("open my notes file")
	// arrives as a NAME, not a path, and the planner has no way to know where
	// it lives. Requiring file_path made those commands impossible — the model
	// either omitted it and the call failed, or guessed a path and opened
	// something else entirely.
	if path == "" && strings.TrimSpace(params.Query) != "" {
		if found := resolveFile(params.Query); found != "" {
			path = found
		} else {
			return "", fmt.Errorf("could not find a file matching %q", params.Query)
		}
	}
	if path == "" {
		return "", fmt.Errorf("missing file_path parameter")
	}

	cmd := exec.Command("cmd.exe", "/c", "start", `""`, path)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}

	return fmt.Sprintf("Successfully opened file: %s", path), nil
}

// resolveFile finds a file by the name a person actually said. Mirrors
// resolveFolder (open_explorer.go) but selects regular files rather than
// directories, and strips the trailing noun people append when speaking
// ("open my notes FILE").
func resolveFile(query string) string {
	q := cleanFileQuery(query)
	if q == "" {
		return ""
	}
	var files []string
	for _, rec := range search.SearchFiles(q) {
		if info, err := os.Stat(rec.Path); err == nil && !info.IsDir() {
			files = append(files, rec.Path)
		}
	}
	return pickBestDir(files, q) // same scoring: exact name wins, then shallowest
}

var fileNouns = []string{"file", "document", "doc"}

func cleanFileQuery(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	for _, n := range fileNouns {
		q = strings.TrimSpace(strings.TrimSuffix(q, " "+n))
		if q == n {
			q = ""
		}
	}
	for _, lead := range []string{"the ", "my ", "a "} {
		q = strings.TrimPrefix(q, lead)
	}
	return strings.TrimSpace(q)
}
