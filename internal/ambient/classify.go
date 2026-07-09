package ambient

import (
	"path/filepath"
	"regexp"
	"strings"
)

type FileMatch struct{ Icon, Action, Kind string }

var (
	archiveExt   = map[string]bool{".zip": true, ".rar": true, ".7z": true, ".tar": true, ".gz": true}
	imageExt     = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
	installerExt = map[string]bool{".exe": true, ".msi": true}
	partialExt   = map[string]bool{".part": true, ".crdownload": true, ".tmp": true, ".download": true}
)

// ClassifyDownload maps a finished download filename to a suggestion template.
// Returns ok=false for partial downloads and unrecognized types.
func ClassifyDownload(name string) (FileMatch, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	if partialExt[ext] {
		return FileMatch{}, false
	}
	switch {
	case archiveExt[ext]:
		return FileMatch{Icon: "download", Action: "Unzip", Kind: "archive"}, true
	case imageExt[ext]:
		return FileMatch{Icon: "download", Action: "Open", Kind: "image"}, true
	case installerExt[ext]:
		return FileMatch{Icon: "download", Action: "Run", Kind: "installer"}, true
	}
	return FileMatch{}, false
}

type ClipMatch struct{ Icon, Title, Message, Action, Kind, URL string }

var (
	urlRe      = regexp.MustCompile(`^\s*(https?://[^\s]+)\s*$`)
	errorRe    = regexp.MustCompile(`(?i)(panic:|traceback|exception|error:|\bstack trace\b|\bat .*\.(go|js|ts|py):\d+)`)
	trackingRe = regexp.MustCompile(`^\s*(1Z[0-9A-Z]{16}|[0-9]{12,22})\s*$`) // UPS / generic long numeric
)

// ClassifyClipboard recognizes actionable clipboard text (URL / error / tracking).
func ClassifyClipboard(text string) (ClipMatch, bool) {
	if m := urlRe.FindStringSubmatch(text); m != nil {
		return ClipMatch{Icon: "link", Title: "Link copied", Message: truncate(m[1], 48) + " — open it?", Action: "Open", Kind: "url", URL: m[1]}, true
	}
	if trackingRe.MatchString(text) {
		code := strings.TrimSpace(text)
		return ClipMatch{Icon: "link", Title: "Tracking number copied", Message: code + " — track it?", Action: "Track", Kind: "tracking",
			URL: "https://www.google.com/search?q=track+package+" + code}, true
	}
	if errorRe.MatchString(text) && len(text) < 4000 {
		return ClipMatch{Icon: "warn", Title: "Error copied", Message: "Explain this error?", Action: "Explain", Kind: "error"}, true
	}
	return ClipMatch{}, false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
