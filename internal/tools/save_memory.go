package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/internal/memory"
)

// SaveMemoryTool is the legacy KV memory tool — kept for backward compatibility.
type SaveMemoryTool struct {
	Store *memory.Store
}

func (s *SaveMemoryTool) Name() string               { return "save_memory" }
func (s *SaveMemoryTool) RequiresConfirmation() bool { return false }
func (s *SaveMemoryTool) Description() string {
	return "Saves a key/value pair to the agent's persistent memory"
}
func (s *SaveMemoryTool) Parameters() string {
	return `{"key": {"type": "string"}, "value": {"type": "string"}}`
}

type SaveMemoryArgs struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (s *SaveMemoryTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params SaveMemoryArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	key := params.Key
	value := params.Value
	if key == "" || value == "" {
		return "", fmt.Errorf("save_memory: key and value are required")
	}
	if err := s.Store.KVSave(key, value); err != nil {
		return "", err
	}
	return "Memory saved: " + key, nil
}
