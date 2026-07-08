package tools

import (
	"encoding/json"
	"testing"
)

func TestWindowControlSchemaValid(t *testing.T) {
	var v map[string]interface{}
	if err := json.Unmarshal([]byte((&WindowControlTool{}).Parameters()), &v); err != nil {
		t.Fatalf("invalid schema: %v", err)
	}
}

func TestWindowControlComboLookup(t *testing.T) {
	// Pure mapping check — does not actually send keys.
	if _, ok := windowCombo["snap_left"]; !ok {
		t.Fatal("snap_left must map to a key combo")
	}
	if _, ok := windowCombo["nope"]; ok {
		t.Fatal("unknown action must not map")
	}
}
