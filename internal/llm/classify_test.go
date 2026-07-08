package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// TestOpenAICompatibleClassifyAndPlan drives ClassifyAndPlan against a fake
// HTTP server to verify actual behavior (not just prompt string contents):
// needs_screen=true/false parsing and the safe fallback on garbage content.
func TestOpenAICompatibleClassifyAndPlan(t *testing.T) {
	tests := []struct {
		name            string
		messageContent  string
		wantNeedsScreen bool
		wantRawJSONSet  bool
	}{
		{
			name:            "needs_screen true",
			messageContent:  `{"needs_screen": true}`,
			wantNeedsScreen: true,
			wantRawJSONSet:  false,
		},
		{
			name:            "needs_screen false with tasks",
			messageContent:  `{"needs_screen": false, "intent":"open_app", "tasks":[{"tool":"open_app","params":{"name":"notepad"}}]}`,
			wantNeedsScreen: false,
			wantRawJSONSet:  true,
		},
		{
			name:            "garbage content falls back safe",
			messageContent:  `not json at all`,
			wantNeedsScreen: true,
			wantRawJSONSet:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/chat/completions" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				resp := map[string]interface{}{
					"choices": []map[string]interface{}{
						{
							"message": map[string]interface{}{
								"content": tt.messageContent,
							},
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(resp); err != nil {
					t.Errorf("failed to encode fake response: %v", err)
				}
			}))
			defer server.Close()

			provider := NewOpenAICompatible("fake-key", "gpt-4o", server.URL)

			got, err := provider.ClassifyAndPlan(context.Background(), "do something", "{}", "ctx")
			if err != nil {
				t.Fatalf("ClassifyAndPlan returned error: %v", err)
			}

			if got.NeedsScreen != tt.wantNeedsScreen {
				t.Errorf("NeedsScreen = %v, want %v", got.NeedsScreen, tt.wantNeedsScreen)
			}

			if tt.wantRawJSONSet && got.RawJSON == "" {
				t.Errorf("expected RawJSON to be non-empty, got empty")
			}
			if !tt.wantRawJSONSet && got.RawJSON != "" {
				t.Errorf("expected RawJSON to be empty, got %q", got.RawJSON)
			}
		})
	}
}
