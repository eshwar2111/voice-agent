package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// openWithDefaultApp launches path with its default handler.
//
// It deliberately avoids cmd.exe's `start` builtin: opening a file needs an
// empty title argument (`start "" "file"`), but Go's Windows arg escaping turns
// the literal "" into `"\"\""`, which `start` mis-parses as a path of `\` —
// producing the "Windows cannot find '\\'" dialog. rundll32's
// FileProtocolHandler opens any file or URL with its default program and takes
// a single, cleanly-escaped path argument, so it has none of that ambiguity.
// (This is the same entry point the auth/ambient code already uses.)
func openWithDefaultApp(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("no path to open")
	}
	// A clear error beats a bare Windows dialog when the path is wrong.
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("path not found: %s", path)
	}
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
}

// knownFolders maps common spoken/typed folder names to their location under the
// user's home directory ("" means home itself). Case-insensitive on the caller.
var knownFolders = map[string]string{
	"downloads": "Downloads",
	"download":  "Downloads",
	"desktop":   "Desktop",
	"documents": "Documents",
	"docs":      "Documents",
	"pictures":  "Pictures",
	"photos":    "Pictures",
	"music":     "Music",
	"videos":    "Videos",
	"video":     "Videos",
	"home":      "",
}

// resolveUserPath expands a loosely-specified path into a real one:
//   - "~" / "~/x"                 → the home dir (and below)
//   - "downloads", "desktop", …   → the matching known folder under home
//   - "downloads/reports"         → known folder + sub-path
//   - %VAR% and $VAR              → environment variables
// An already-absolute or unrecognised path is returned unchanged (so a genuinely
// bad path still surfaces its error). This is why "keep it in downloads" used to
// fail — "downloads" was taken literally instead of the user's Downloads folder.
func resolveUserPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	// Environment variables first (both %WINDOWS% and $unix styles).
	p = expandWinEnv(os.ExpandEnv(p))

	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}

	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		return filepath.Join(home, filepath.FromSlash(strings.TrimLeft(p[1:], `/\`)))
	}
	// Bare drive "E:" is drive-RELATIVE on Windows (current dir on E:), almost
	// never what's meant — normalise to the drive root "E:\".
	if len(p) == 2 && p[1] == ':' && isDriveLetter(p[0]) {
		return strings.ToUpper(p[:1]) + `:\`
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}

	// First path segment as a known-folder alias.
	norm := strings.ReplaceAll(p, `\`, "/")
	parts := strings.SplitN(norm, "/", 2)
	if sub, ok := knownFolders[strings.ToLower(strings.TrimSpace(parts[0]))]; ok {
		base := home
		if sub != "" {
			base = filepath.Join(home, sub)
		}
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			return filepath.Join(base, filepath.FromSlash(parts[1]))
		}
		return base
	}
	return p
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// driveRoot returns "X:\" if s names a drive — "e", "E:", "e drive", "the e
// folder" — and that drive actually exists; otherwise "". This is why "open e
// folder" used to fail: it was searched as a folder named "E folder" instead of
// being read as the E: drive.
func driveRoot(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	for _, w := range []string{"the ", "my "} {
		t = strings.TrimPrefix(t, w)
	}
	for _, w := range []string{" drive", " folder", " directory", " dir"} {
		t = strings.TrimSuffix(t, w)
	}
	t = strings.TrimSpace(strings.TrimSuffix(t, ":"))
	if len(t) == 1 && isDriveLetter(t[0]) {
		root := strings.ToUpper(t) + `:\`
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	return ""
}

// expandWinEnv expands %VAR% references (os.ExpandEnv only handles $VAR).
func expandWinEnv(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "%")
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.Index(s[i+1:], "%")
		if j < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		name := s[i+1 : i+1+j]
		if v, ok := os.LookupEnv(name); ok {
			b.WriteString(v)
		} else {
			b.WriteString("%" + name + "%") // leave unknown refs intact
		}
		s = s[i+1+j+1:]
	}
	return b.String()
}
