package tools

import (
	"encoding/json"
	"testing"
)

func TestSystemControlSchemaValid(t *testing.T) {
	var v map[string]interface{}
	if err := json.Unmarshal([]byte((&SystemControlTool{}).Parameters()), &v); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestSystemControlRejectsUnknown(t *testing.T) {
	_, err := (&SystemControlTool{}).Execute(nil, json.RawMessage(`{"action":"selfdestruct"}`))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}
