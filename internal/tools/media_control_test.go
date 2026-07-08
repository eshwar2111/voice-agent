package tools

import (
	"encoding/json"
	"testing"
)

func TestMediaControlSchemaValid(t *testing.T) {
	var v map[string]interface{}
	if err := json.Unmarshal([]byte((&MediaControlTool{}).Parameters()), &v); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestMediaControlRejectsUnknownAction(t *testing.T) {
	_, err := (&MediaControlTool{}).Execute(nil, json.RawMessage(`{"action":"explode"}`))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}
