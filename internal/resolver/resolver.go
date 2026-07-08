package resolver

import "strings"

// NormalizedInput is the pre-processed command handed to every Matcher.
type NormalizedInput struct {
	Raw       string   // original text, unchanged
	Lower     string   // lowercased, single-spaced, trimmed
	Tokens    []string // whitespace-split tokens of Lower
	ActiveApp string   // foreground process name, may be ""
}

// Normalize prepares raw user text for matching. activeApp may be "".
func Normalize(raw, activeApp string) NormalizedInput {
	lower := strings.ToLower(strings.TrimSpace(raw))
	tokens := strings.Fields(lower)
	return NormalizedInput{
		Raw:       raw,
		Lower:     strings.Join(tokens, " "),
		Tokens:    tokens,
		ActiveApp: activeApp,
	}
}
