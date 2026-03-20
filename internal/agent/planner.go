package agent

import (
	"encoding/json"
	"strings"
)

type Task struct {
	Tool   string          `json:"tool"`
	Params json.RawMessage `json:"params"`
}

type Plan struct {
	Transcript string `json:"transcript"`
	Intent     string `json:"intent"`
	Reason     string `json:"reason"`
	Tasks      []Task `json:"tasks"`
}

// CreatePlan translates a high-level intent into a sequence of individual tool executions (The Task Graph).
func CreatePlan(intent string, params map[string]string) Plan {
	intent = strings.ToLower(strings.TrimSpace(intent))

	switch intent {
	case "explain_screen_error":
		return Plan{
			Intent: intent,
			Tasks: []Task{
				{Tool: "analyze_screen", Params: json.RawMessage(`{}`)},
				{Tool: "speak", Params: json.RawMessage(`{"text": "I have analyzed the screen. Explaining the error now."}`)},
			},
		}

	case "rewrite_clipboard_email":
		return Plan{
			Intent: intent,
			Tasks: []Task{
				{Tool: "rewrite_clipboard", Params: json.RawMessage(`{"style": "professional email"}`)},
				{Tool: "speak", Params: json.RawMessage(`{"text": "I have rewritten the clipboard contents into a professional email."}`)},
			},
		}

	case "summarize_screen_notes":
		return Plan{
			Intent: intent,
			Tasks: []Task{
				{Tool: "analyze_screen", Params: json.RawMessage(`{}`)},
				{Tool: "create_file", Params: json.RawMessage(`{"filename": "screen_notes.txt", "content": "{PREVIOUS_OUTPUT}"}`)},
				{Tool: "speak", Params: json.RawMessage(`{"text": "I have summarized the screen and saved it to meeting notes."}`)},
			},
		}

	}

	var rawParams json.RawMessage
	if len(params) > 0 {
		if b, err := json.Marshal(params); err == nil {
			rawParams = b
		}
	} else {
		rawParams = json.RawMessage(`{}`)
	}

	// Default: if it's not a multi-step automation, just map the single intent directly to the tool graph
	return Plan{
		Intent: intent,
		Tasks: []Task{
			{Tool: intent, Params: rawParams},
		},
	}
}
