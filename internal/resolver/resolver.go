package resolver

import (
	"strings"

	"github.com/yourname/voice-agent/internal/agent"
)

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
	// Strip trailing sentence punctuation. Whisper transcribes speech WITH
	// punctuation — "Open Notepad." not "open notepad" — and every matcher here
	// compares against bare words, so the trailing period made the app lookup
	// search for "notepad." and miss. That silently bypassed Tier 0 for
	// essentially every spoken command, sending it to the cloud orchestrator
	// instead: slower, costs tokens, and in practice it hallucinated a tool
	// name ("open_application") that is not in the registry, so the plan failed
	// outright. Only the tail is trimmed, so internal dots survive — "open
	// report.txt" keeps its extension.
	lower = strings.TrimRight(lower, " .,!?;:")
	tokens := strings.Fields(lower)
	return NormalizedInput{
		Raw:       raw,
		Lower:     strings.Join(tokens, " "),
		Tokens:    tokens,
		ActiveApp: activeApp,
	}
}

// DefaultThreshold is the minimum confidence for a local (Tier 0) match.
const DefaultThreshold = 0.7

// Match is a resolved local plan with a confidence score.
type Match struct {
	Tasks      []agent.Task
	Confidence float64
	Reason     string
}

// Matcher recognizes one intent domain deterministically.
type Matcher interface {
	Name() string
	Match(in NormalizedInput) (*Match, bool)
}

// Resolver runs matchers in priority order and returns the first qualifying match.
type Resolver struct {
	Matchers  []Matcher
	Threshold float64
}

// NewResolver creates a Resolver with the given matchers and default threshold.
func NewResolver(matchers ...Matcher) *Resolver {
	return &Resolver{Matchers: matchers, Threshold: DefaultThreshold}
}

// Resolve returns the first match whose confidence >= Threshold, else (nil,false).
func (r *Resolver) Resolve(in NormalizedInput) (*Match, bool) {
	for _, m := range r.Matchers {
		if match, ok := m.Match(in); ok && match != nil && match.Confidence >= r.Threshold {
			return match, true
		}
	}
	return nil, false
}
