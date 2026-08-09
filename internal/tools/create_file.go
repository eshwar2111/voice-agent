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
	return "Creates a file. Optionally writes initial content into it. Refuses to overwrite an existing file unless overwrite is true."
}

func (c *CreateFileTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"filename": {"type": "string", "description": "File name (e.g. 'notes.txt') or an absolute path. Required."},
			"path": {"type": "string", "description": "Directory to create the file in. Defaults to the user's Desktop when omitted and filename is not absolute."},
			"content": {"type": "string", "description": "Initial contents of the file. Omit for an empty file."},
			"overwrite": {"type": "boolean", "description": "Replace the file if it already exists. Default false."}
		},
		"required": ["filename"]
	}`
}

func (c *CreateFileTool) RequiresConfirmation() bool {
	return true // Write actions might require confirmation
}

type CreateFileArgs struct {
	Path      string `json:"path"`
	Filename  string `json:"filename"`
	FileName  string `json:"file_name"` // Alias
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite"`
}

// isRestricted reports whether a resolved path lands inside a directory we
// refuse to write to. Checked against the FINAL path, not the caller's `path`
// argument — the old check only inspected `path`, so an absolute filename
// pointing at C:\Windows\System32 walked straight past it.
func isRestricted(p string) bool {
	lower := strings.ToLower(p)
	for _, restricted := range RestrictedPaths {
		if strings.Contains(lower, restricted) {
			return true
		}
	}
	return false
}

func (c *CreateFileTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params CreateFileArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	filename := strings.TrimSpace(params.Filename)
	if filename == "" {
		filename = strings.TrimSpace(params.FileName) // Attempt alias
	}
	if filename == "" {
		return "", errors.New("create_file requires a filename")
	}

	var fullPath string
	switch {
	case filepath.IsAbs(filename):
		// The schema has always advertised that filename may be an absolute
		// path, but the old code joined it onto a directory regardless, which
		// produces a nonsense path on Windows.
		fullPath = filepath.Clean(filename)
	default:
		dir := strings.TrimSpace(params.Path)
		if dir == "" {
			// Defaulting to the process working directory put files next to the
			// executable, where the user would never find them. The Desktop is
			// where someone who says "create a file" expects it to appear.
			if home, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(home, "Desktop")
				if info, err := os.Stat(dir); err != nil || !info.IsDir() {
					dir = home
				}
			} else if cwd, err := os.Getwd(); err == nil {
				dir = cwd
			} else {
				dir = "."
			}
		} else if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return "", fmt.Errorf("the specified path does not exist or is not a directory: %s", dir)
		}
		fullPath = filepath.Join(dir, filename)
	}

	if isRestricted(fullPath) {
		return "", errors.New("action blocked: path contains restricted directory")
	}

	// Never clobber silently. os.WriteFile truncates, so "create notes.txt" used
	// to erase an existing notes.txt and then report success — the user lost the
	// file and was told the operation worked.
	if _, err := os.Stat(fullPath); err == nil && !params.Overwrite {
		return "", fmt.Errorf("%s already exists — pass overwrite:true to replace it", fullPath)
	}

	if err := os.WriteFile(fullPath, []byte(params.Content), 0644); err != nil {
		return "", err
	}

	// Reveal the file, but never fail the operation over it. The file exists at
	// this point; a missing or busy explorer.exe is not a reason to report the
	// creation as failed and send the planner into recovery.
	if cmd := exec.Command("explorer", "/select,", fullPath); cmd.Start() == nil {
		_ = cmd.Process.Release()
	}

	if params.Content == "" {
		return fmt.Sprintf("Created empty file at %s", fullPath), nil
	}
	return fmt.Sprintf("Created %s (%d bytes)", fullPath, len(params.Content)), nil
}
