package llm

import (
	"strings"
	"testing"
)

// The classify system prompt must instruct the model to emit needs_screen,
// and both non-Gemini providers must build their classify request from it
// (not from the planning prompt).
func TestClassifySystemPromptMentionsNeedsScreen(t *testing.T) {
	if !strings.Contains(classifySystemPrompt, "needs_screen") {
		t.Fatalf("classifySystemPrompt must reference needs_screen")
	}
}

// Guard against regressing to the planning prompt in the classify path.
// buildClassifyPrompt is the shared helper the providers must call.
func TestBuildClassifyPromptUsesClassifyPrompt(t *testing.T) {
	got := buildClassifyPrompt("{}", "ctx")
	if !strings.Contains(got, "needs_screen") {
		t.Fatalf("buildClassifyPrompt must be derived from classifySystemPrompt")
	}
}
