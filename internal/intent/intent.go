package intent

import "encoding/json"

type Task struct {
	Tool   string          `json:"tool"`
	Params json.RawMessage `json:"params"`
}

type ParsedIntent struct {
	Intent     string          `json:"intent"`
	Parameters json.RawMessage `json:"parameters,omitempty"` // Legacy fallback
	Tasks      []Task          `json:"tasks"`                // New Phase 5
	Reason     string          `json:"reason,omitempty"`
}

func ParseIntentJSON(rawJSON string) (ParsedIntent, error) {
	var i ParsedIntent
	err := json.Unmarshal([]byte(rawJSON), &i)
	return i, err
}
