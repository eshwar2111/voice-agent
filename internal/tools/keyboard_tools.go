package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yourname/voice-agent/internal/automation"
	"github.com/yourname/voice-agent/internal/ui"
)

// KeyboardTypeTool types a string of text at the current cursor position.
type KeyboardTypeTool struct{}

func (t *KeyboardTypeTool) Name() string               { return "keyboard_type" }
func (t *KeyboardTypeTool) RequiresConfirmation() bool { return false }
func (t *KeyboardTypeTool) Description() string {
	return "Type a string of text at the current cursor position."
}
func (t *KeyboardTypeTool) Parameters() string {
	return `{"text": {"type": "string", "description": "The text to type"}}`
}

type KeyboardTypeArgs struct {
	Text string `json:"text"`
}

func (t *KeyboardTypeTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params KeyboardTypeArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	text := params.Text
	if text == "" {
		return "", fmt.Errorf("keyboard_type: 'text' is required")
	}
	preview := text
	if len(preview) > 40 {
		preview = preview[:40] + "..."
	}
	ui.ShowAutomationStep(fmt.Sprintf("Typing: \"%s\"", preview))
	if err := automation.TypeText(text); err != nil {
		return "", err
	}
	return fmt.Sprintf("Typed %d characters", len(text)), nil
}

// KeyboardPressTool presses a single key.
type KeyboardPressTool struct{}

func (t *KeyboardPressTool) Name() string               { return "keyboard_press" }
func (t *KeyboardPressTool) RequiresConfirmation() bool { return false }
func (t *KeyboardPressTool) Description() string {
	return "Press a single keyboard key (e.g. 'enter', 'escape', 'tab', 'backspace', 'space')."
}
func (t *KeyboardPressTool) Parameters() string {
	return `{"key": {"type": "string", "description": "Key name e.g. enter, escape, tab, space, backspace, up, down, left, right"}}`
}

type KeyboardPressArgs struct {
	Key string `json:"key"`
}

func (t *KeyboardPressTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params KeyboardPressArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	key := params.Key
	if key == "" {
		return "", fmt.Errorf("keyboard_press: 'key' is required")
	}
	ui.ShowAutomationStep(fmt.Sprintf("Pressing key: %s", key))
	if err := automation.PressKey(key); err != nil {
		return "", err
	}
	return fmt.Sprintf("Pressed key: %s", key), nil
}

// KeyboardComboTool sends a keyboard shortcut (e.g. Ctrl+C, Alt+F4).
type KeyboardComboTool struct{}

func (t *KeyboardComboTool) Name() string               { return "keyboard_combo" }
func (t *KeyboardComboTool) RequiresConfirmation() bool { return false }
func (t *KeyboardComboTool) Description() string {
	return "Press a keyboard shortcut combination. Provide keys as a comma-separated string, e.g. 'ctrl,c' or 'alt,f4'."
}
func (t *KeyboardComboTool) Parameters() string {
	return `{"keys": {"type": "string", "description": "Comma-separated key names, e.g. 'ctrl,c' or 'ctrl,shift,s'"}}`
}

type KeyboardComboArgs struct {
	Keys string `json:"keys"`
}

func (t *KeyboardComboTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params KeyboardComboArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	raw := params.Keys
	if raw == "" {
		return "", fmt.Errorf("keyboard_combo: 'keys' is required")
	}
	keys := strings.Split(raw, ",")
	for i := range keys {
		keys[i] = strings.TrimSpace(keys[i])
	}
	ui.ShowAutomationStep(fmt.Sprintf("Key combo: %s", strings.Join(keys, "+")))
	if err := automation.PressCombo(keys); err != nil {
		return "", err
	}
	return fmt.Sprintf("Executed combo: %s", strings.Join(keys, "+")), nil
}

// WaitTool pauses automation execution for a specified number of seconds.
type WaitTool struct{}

func (t *WaitTool) Name() string               { return "wait" }
func (t *WaitTool) RequiresConfirmation() bool { return false }
func (t *WaitTool) Description() string {
	return "Pause execution for a number of seconds. Useful between automation steps for UI to load."
}
func (t *WaitTool) Parameters() string {
	return `{"seconds": {"type": "string", "description": "Number of seconds to wait (can be decimal, e.g. 0.5, 1, 2)"}}`
}

type WaitArgs struct {
	Seconds string `json:"seconds"`
}

func (t *WaitTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params WaitArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	secs, err := strconv.ParseFloat(params.Seconds, 64)
	if err != nil || secs <= 0 {
		secs = 1.0
	}
	if secs > 30 {
		secs = 30 // safety cap
	}
	ui.ShowAutomationStep(fmt.Sprintf("Waiting %.1f second(s)...", secs))
	if err := automation.SleepCtx(ctx, secs); err != nil {
		return "", err
	}
	return fmt.Sprintf("Waited %.1f seconds", secs), nil
}
