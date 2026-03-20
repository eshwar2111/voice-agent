package executor

import (
	"fmt"
	"syscall"
	"time"

	"github.com/atotto/clipboard"
)

// SimulateCtrlC uses native Windows user32.dll to send a temporary Ctrl+C keystroke to copy highlighted text.
func SimulateCtrlC() (string, error) {
	fmt.Println("Simulating Ctrl+C to copy selected text...")

	user32 := syscall.NewLazyDLL("user32.dll")
	keybdEvent := user32.NewProc("keybd_event")

	const VK_CONTROL = 0x11
	const VK_C = 0x43
	const KEYEVENTF_KEYUP = 0x0002

	// Press Ctrl
	keybdEvent.Call(uintptr(VK_CONTROL), 0, 0, 0)
	// Press C
	keybdEvent.Call(uintptr(VK_C), 0, 0, 0)
	// Release C
	keybdEvent.Call(uintptr(VK_C), 0, KEYEVENTF_KEYUP, 0)
	// Release Ctrl
	keybdEvent.Call(uintptr(VK_CONTROL), 0, KEYEVENTF_KEYUP, 0)

	// Wait briefly for the OS clipboard buffer to catch up
	time.Sleep(150 * time.Millisecond)

	text, err := clipboard.ReadAll()
	if err != nil {
		return "", fmt.Errorf("failed to read clipboard: %v", err)
	}

	// Truncate to avoid blowing up the LLM context if they accidentally highlight a whole novel
	if len(text) > 3000 {
		text = text[:3000] + "\n...(truncated)"
	}

	return text, nil
}
