package automation

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-vgo/robotgo"
)

// TypeText types the given string character by character.
// Uses a small delay between characters to prevent dropped inputs in fast apps.
func TypeText(text string) error {
	expectedPID := GetActivePID()
	if err := VerifyFocusLock(expectedPID); err != nil {
		return err
	}
	robotgo.TypeStr(text)
	return nil
}

// PressKey presses and releases a single key (e.g. "enter", "tab", "escape", "backspace").
func PressKey(key string) error {
	expectedPID := GetActivePID()
	if err := VerifyFocusLock(expectedPID); err != nil {
		return err
	}
	robotgo.KeyTap(strings.ToLower(key))
	return nil
}

// PressCombo sends a key chord (e.g. ["ctrl","c"], ["alt","f4"]).
// The last key in the slice is the primary key; all others are modifiers.
func PressCombo(keys []string) error {
	if len(keys) == 0 {
		return fmt.Errorf("keyboard_combo: no keys provided")
	}

	expectedPID := GetActivePID()
	if err := VerifyFocusLock(expectedPID); err != nil {
		return err
	}

	primary := strings.ToLower(keys[len(keys)-1])
	modifiers := make([]interface{}, len(keys)-1)
	for i, k := range keys[:len(keys)-1] {
		modifiers[i] = strings.ToLower(k)
	}

	if len(modifiers) == 0 {
		robotgo.KeyTap(primary)
	} else {
		robotgo.KeyTap(primary, modifiers...)
	}

	time.Sleep(30 * time.Millisecond)
	return nil
}
