package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yourname/voice-agent/internal/ui"
)

// This file holds the shared "ask, don't guess" logic used by open_file /
// open_explorer / open_app: when a spoken name genuinely matches SEVERAL
// candidates, ask the user which one via a compact island choice instead of
// silently picking. A single clear match resolves immediately (no needless
// question); when nothing strongly matches it falls back to a best guess rather
// than nagging. The multi-turn state stays internal — the UI never becomes a chat.

// queryTokens splits a cleaned spoken name into meaningful lowercase tokens.
func queryTokens(q string) []string {
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

// nameMatchesAllTokens reports whether name (separators normalized) contains
// every query token — a genuine match, not an incidental index hit.
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

// pickPathOrAsk resolves a file/folder path from candidates, asking the user
// when 2+ genuinely match the spoken name q. Returns "" if the user cancels or
// there are no candidates.
func pickPathOrAsk(paths []string, q string) string {
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return paths[0]
	}
	toks := queryTokens(q)
	var strong []string
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		if nameMatchesAllTokens(filepath.Base(p), toks) {
			strong = append(strong, p)
		}
	}
	switch {
	case len(strong) == 1:
		return strong[0]
	case len(strong) >= 2:
		return askAmongPaths(strong, q) // "" on cancel — don't open the wrong thing
	default:
		return pickBestDir(paths, q) // nothing strongly matches; best guess, no nag
	}
}

// askAmongPaths presents up to 5 path candidates (name + folder) as a compact
// choice and returns the chosen path, or "" on cancel / no UI.
func askAmongPaths(paths []string, q string) string {
	if len(paths) > 5 {
		paths = paths[:5]
	}
	opts := make([]ui.Option, 0, len(paths))
	for _, p := range paths {
		opts = append(opts, ui.Option{ID: p, Label: filepath.Base(p), Sub: filepath.Dir(p)})
	}
	id, ok := ui.AskChoice(fmt.Sprintf("Which %q do you mean?", q), opts)
	if ok {
		return id
	}
	return ""
}
