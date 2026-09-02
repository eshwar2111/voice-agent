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

	"github.com/yourname/voice-agent/internal/search"
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
			"path": { "type": "string", "description": "Optional. An absolute directory path, if you already know it." },
			"query": { "type": "string", "description": "Optional. The folder NAME the user said, e.g. \"voice agent\" for \"open the voice agent folder\". Use this whenever the user names a folder but you do not know its path. Omit both to just open File Explorer." }
		},
		"required": []
	}`
}

func (o *OpenExplorerTool) RequiresConfirmation() bool {
	return false
}

type OpenExplorerArgs struct {
	Path string `json:"path"`
	Query string `json:"query"`
}

func (o *OpenExplorerTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params OpenExplorerArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	// An explicit path may be a drive ("E:"), an alias ("downloads"), "~/x", or a
	// real absolute path — resolve all of those.
	path := strings.TrimSpace(params.Path)
	if path != "" {
		if d := driveRoot(path); d != "" {
			path = d
		} else {
			path = resolveUserPath(path)
		}
	}

	// A spoken folder name ("open the E folder", "open downloads", "open voice
	// agent folder") arrives as `query`. Try, in order: a drive letter, a
	// known-folder alias, then the file index.
	if path == "" {
		q := strings.TrimSpace(params.Query)
		if q != "" {
			switch {
			case driveRoot(q) != "":
				path = driveRoot(q)
			case aliasDir(q) != "":
				path = aliasDir(q)
			default:
				if found := resolveFolder(q); found != "" {
					path = found
				} else {
					// A named folder we cannot find must FAIL, not silently open
					// the default window (a wrong result is worse than a clear
					// error).
					return "", fmt.Errorf("could not find a folder matching %q", q)
				}
			}
		}
	}

	if path == "" {
		// Genuinely nothing named — "Open File Explorer" is a complete request
		// on its own. Bare `explorer` opens the default window.
		fmt.Println("Opening explorer (no folder named)")
		if err := exec.Command("explorer").Start(); err != nil {
			return "", err
		}
		return "Explorer opened", nil
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

// aliasDir resolves a spoken folder alias ("downloads", "my desktop") to a real
// existing directory via resolveUserPath, or "" if it isn't a known alias.
func aliasDir(query string) string {
	a := cleanFolderQuery(query)
	if a == "" {
		return ""
	}
	if r := resolveUserPath(a); r != a {
		if info, err := os.Stat(r); err == nil && info.IsDir() {
			return r
		}
	}
	return ""
}

// folderNouns are the words people append when naming a directory out loud —
// "open voice agent FOLDER". They are not part of the name, and leaving them
// in makes the index lookup miss every time.
var folderNouns = []string{"folder", "directory", "dir"}

// cleanFolderQuery reduces a spoken folder reference to the bare name.
func cleanFolderQuery(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	for _, n := range folderNouns {
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

// pickBestDir chooses the most plausible directory from candidates: an exact
// name match wins, otherwise the shallowest path. A deeply nested near-match
// is almost never what someone meant.
func pickBestDir(cands []string, query string) string {
	best, bestScore := "", -1
	for _, p := range cands {
		name := strings.ToLower(filepath.Base(p))
		score := 0
		if name == query {
			score = 1000
		}
		score -= strings.Count(p, string(filepath.Separator))
		if score > bestScore {
			best, bestScore = p, score
		}
	}
	return best
}

// resolveFolder searches the file index for a directory matching a spoken
// name. Returns "" when nothing matches or the index is not ready.
func resolveFolder(query string) string {
	q := cleanFolderQuery(query)
	if q == "" {
		return ""
	}
	// Probe obvious locations first. The file index only walks the user
	// profile (cmd/app/main.go), so a project living on another drive —
	// E:\Voice Agent — is invisible to it no matter how well the query is
	// cleaned. Checking the working directory and its neighbours costs a few
	// stat calls and covers the folders someone actually talks about.
	if p := probeCommonDirs(q); p != "" {
		return p
	}
	var dirs []string
	for _, rec := range search.SearchFiles(q) {
		if info, err := os.Stat(rec.Path); err == nil && info.IsDir() {
			dirs = append(dirs, rec.Path)
		}
	}
	return pickBestDir(dirs, q)
}

// probeCommonDirs looks for a directory named q in the handful of places a
// person is likely to mean, without walking the filesystem.
func probeCommonDirs(q string) string {
	var bases []string
	if wd, err := os.Getwd(); err == nil {
		// The working directory itself may BE the folder being named.
		if strings.EqualFold(filepath.Base(wd), q) {
			return wd
		}
		bases = append(bases, wd, filepath.Dir(wd))
	}
	if home, err := os.UserHomeDir(); err == nil {
		bases = append(bases, home,
			filepath.Join(home, "Desktop"),
			filepath.Join(home, "Documents"),
			filepath.Join(home, "Downloads"))
	}
	for _, base := range bases {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.EqualFold(e.Name(), q) {
				return filepath.Join(base, e.Name())
			}
		}
	}
	return ""
}
