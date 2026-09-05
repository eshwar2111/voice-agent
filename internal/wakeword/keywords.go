package wakeword

import "strings"

// selectKeyword returns the full keyword line from a sherpa keywords file whose
// trailing "@label" matches wakeWord (case-insensitive, trimmed). The returned
// line is passed verbatim to NewKeywordStreamWithKeywords so only that phrase is
// armed. ok is false when no line matches; the caller then falls back to the
// first line.
func selectKeyword(fileContent, wakeWord string) (string, bool) {
	want := normLabel(wakeWord)
	for _, raw := range strings.Split(fileContent, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if i := strings.Index(trimmed, "@"); i >= 0 {
			if normLabel(trimmed[i+1:]) == want {
				return trimmed, true
			}
		}
	}
	return "", false
}

// normLabel canonicalizes a keyword label for matching: lowercased, and with
// underscores treated as spaces (collapsed). sherpa's keyword-file format
// requires the "@label" to be a SINGLE token, so multi-word wake words are
// written with underscores (`@hey_jarvis`), while the human-facing `wake_word`
// config value is spaced (`hey jarvis`) — this lets the two match.
func normLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Join(strings.Fields(s), " ")
}

// firstKeyword returns the first non-empty line, used as the fallback when the
// configured wake word is not present in the file.
func firstKeyword(fileContent string) (string, bool) {
	for _, raw := range strings.Split(fileContent, "\n") {
		if s := strings.TrimSpace(strings.TrimRight(raw, "\r")); s != "" {
			return s, true
		}
	}
	return "", false
}
