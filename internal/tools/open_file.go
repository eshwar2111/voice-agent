package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yourname/voice-agent/internal/search"
	"github.com/yourname/voice-agent/internal/ui"
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

	if err := openWithDefaultApp(path); err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}

	// Usage learning: opening a file raises its rank for future resolves.
	search.RecordOpen(path)

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
	return pickFileOrAsk(files, q)
}

// pickFileOrAsk resolves the file to open. When SEVERAL files genuinely match
// the spoken name (e.g. two resumes, or the same filename in two folders) it
// asks the user which one — the "ask, don't guess" principle — via a compact
// choice on the island. A single clear match opens straight away (no needless
// question); when nothing strongly matches it falls back to the best guess
// rather than nagging.
func pickFileOrAsk(files []string, q string) string {
	switch len(files) {
	case 0:
		return ""
	case 1:
		return files[0]
	}
	toks := fileQueryTokens(q)
	var strong []string
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f] {
			continue
		}
		seen[f] = true
		if nameMatchesAllTokens(filepath.Base(f), toks) {
			strong = append(strong, f)
		}
	}
	switch {
	case len(strong) == 1:
		return strong[0]
	case len(strong) >= 2:
		if picked := askWhichFile(strong, q); picked != "" {
			return picked
		}
		return "" // user cancelled — don't open the wrong thing
	default:
		return pickBestDir(files, q) // nothing strongly matches; best guess, no nag
	}
}

// askWhichFile presents up to 5 candidates as a compact choice and returns the
// chosen path (or "" on cancel / no UI).
func askWhichFile(files []string, q string) string {
	if len(files) > 5 {
		files = files[:5]
	}
	opts := make([]ui.Option, 0, len(files))
	for _, f := range files {
		opts = append(opts, ui.Option{ID: f, Label: filepath.Base(f), Sub: filepath.Dir(f)})
	}
	id, ok := ui.AskChoice(fmt.Sprintf("Which %q do you mean?", q), opts)
	if ok {
		return id
	}
	return ""
}

// fileQueryTokens splits the cleaned query into meaningful lowercase tokens.
func fileQueryTokens(q string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return r == ' ' || r == '_' || r == '-' || r == '.'
	}) {
		if len(t) > 1 {
			out = append(out, t)
		}
	}
	return out
}

// nameMatchesAllTokens reports whether the file's base name (separators
// normalized) contains every query token — i.e. it's a genuine match, not an
// incidental index hit.
func nameMatchesAllTokens(name string, toks []string) bool {
	if len(toks) == 0 {
		return false
	}
	norm := strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(strings.ToLower(name))
	for _, t := range toks {
		if !strings.Contains(norm, t) {
			return false
		}
	}
	return true
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
