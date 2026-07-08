package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/internal/automation"
)

type WindowControlTool struct{}

func (t *WindowControlTool) Name() string             { return "window_control" }
func (t *WindowControlTool) Description() string       { return "Manages the focused window (minimize, maximize, snap, close, switch)." }
func (t *WindowControlTool) RequiresConfirmation() bool { return false }
func (t *WindowControlTool) Parameters() string {
	return `{"type":"object","properties":{"action":{"type":"string","enum":["minimize","maximize","close","snap_left","snap_right","switch"]}},"required":["action"]}`
}

// robotgo combos; PressCombo treats the LAST element as the primary key.
// "cmd" is robotgo's name for the Windows/Super key.
var windowCombo = map[string][]string{
	"minimize":  {"cmd", "down"},
	"maximize":  {"cmd", "up"},
	"snap_left": {"cmd", "left"},
	"snap_right": {"cmd", "right"},
	"close":     {"alt", "f4"},
	"switch":    {"alt", "tab"},
}

type windowArgs struct {
	Action string `json:"action"`
}

func (t *WindowControlTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a windowArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	combo, ok := windowCombo[a.Action]
	if !ok {
		return "", fmt.Errorf("unknown window action: %q", a.Action)
	}
	if err := automation.PressCombo(combo); err != nil {
		return "", err
	}
	return fmt.Sprintf("window: %s", a.Action), nil
}
