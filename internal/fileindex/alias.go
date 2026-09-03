// This file implements heuristic alias derivation (Task 5 of
// docs/superpowers/plans/2026-09-03-file-intelligence-index.md): tokenizing a
// filename (camelCase/snake/kebab/space split, date/version noise stripped) plus
// a small keyword map. The result feeds both the aliases table and the FTS
// keywords column, and is checked against a query to set AliasMatch in ranking.
package fileindex

import (
	"strings"
	"unicode"
)

// keywordGroups maps a filename token to a set of aliases people say for the
// same concept, so "Resume.pdf" also answers to "cv" and "job".
var keywordGroups = map[string][]string{
	"resume":  {"resume", "cv", "job"},
	"cv":      {"resume", "cv", "job"},
	"invoice": {"invoice", "bill"},
	"bill":    {"invoice", "bill"},
	"budget":  {"budget", "finance"},
	"finance": {"budget", "finance"},
}

// deriveAliases tokenizes name (splitting camelCase/snake/kebab/space, stripping
// the extension and date/version noise, lowercasing) and expands keyword-map
// hits. It returns distinct aliases; the same list, space-joined, is the FTS
// keywords column.
func deriveAliases(name string) []string {
	toks := tokenizeName(name)

	out := make([]string, 0, len(toks))
	seen := make(map[string]bool)
	add := func(a string) {
		if a == "" || seen[a] {
			return
		}
		seen[a] = true
		out = append(out, a)
	}

	for _, t := range toks {
		if isNoiseToken(t) {
			continue
		}
		add(t)
		if grp, ok := keywordGroups[t]; ok {
			for _, g := range grp {
				add(g)
			}
		}
	}
	return out
}

// tokenizeName strips the extension, splits on filename separators and camelCase
// boundaries, and lowercases each token.
func tokenizeName(name string) []string {
	base := name
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}

	fields := strings.FieldsFunc(base, func(r rune) bool {
		return r == '_' || r == '-' || r == ' ' || r == '.' ||
			r == '(' || r == ')' || r == '[' || r == ']' || r == '+' || r == ','
	})

	var toks []string
	for _, f := range fields {
		for _, part := range splitCamel(f) {
			toks = append(toks, strings.ToLower(part))
		}
	}
	return toks
}

// splitCamel breaks a run like "voiceAgentNotes" into ["voice","Agent","Notes"]
// on lower→upper transitions (and letter↔digit transitions).
func splitCamel(s string) []string {
	runes := []rune(s)
	if len(runes) == 0 {
		return nil
	}
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := (unicode.IsLower(prev) && unicode.IsUpper(cur)) ||
			(unicode.IsLetter(prev) && unicode.IsDigit(cur)) ||
			(unicode.IsDigit(prev) && unicode.IsLetter(cur))
		if boundary {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

// isNoiseToken drops single characters, pure numbers (dates/counts), version
// markers (v2, v10), and common version words that add no search value.
func isNoiseToken(t string) bool {
	if len(t) <= 1 {
		return true
	}
	if isAllDigits(t) {
		return true
	}
	switch t {
	case "copy", "final", "draft", "version", "ver", "rev":
		return true
	}
	if t[0] == 'v' && len(t) > 1 && isAllDigits(t[1:]) {
		return true
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// normalizeKey lowercases, trims, and collapses internal whitespace so hot-cache
// keys (explicit memory + aliases) compare consistently.
func normalizeKey(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
