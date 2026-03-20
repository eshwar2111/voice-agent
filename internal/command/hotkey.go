package command

import (
	"context"
	"log"

	"github.com/moutend/go-hook/pkg/keyboard"
	"github.com/moutend/go-hook/pkg/types"
	"github.com/yourname/voice-agent/internal/ui"
)

var cancelFunc context.CancelFunc

// SetCancelFunc sets the function to call when Ctrl+Esc is pressed
func SetCancelFunc(cf context.CancelFunc) {
	cancelFunc = cf
}

func ListenHotkey() {
	keyboardChan := make(chan types.KeyboardEvent, 100)

	if err := keyboard.Install(nil, keyboardChan); err != nil {
		log.Printf("Failed to install keyboard hook: %v", err)
		return
	}
	defer keyboard.Uninstall()

	log.Println("Global hotkey listener started. Press Ctrl+Space to open Command Palette.")

	ctrlDown := false

	for event := range keyboardChan {
		// Track Control key state
		if event.VKCode == types.VK_LCONTROL || event.VKCode == types.VK_RCONTROL || event.VKCode == types.VK_CONTROL {
			if event.Message == types.WM_KEYDOWN || event.Message == types.WM_SYSKEYDOWN {
				ctrlDown = true
			} else if event.Message == types.WM_KEYUP || event.Message == types.WM_SYSKEYUP {
				ctrlDown = false
			}
		}

		// Check for Ctrl + Space (Command Palette)
		if event.Message == types.WM_KEYDOWN && event.VKCode == types.VK_SPACE && ctrlDown {
			log.Println("Command palette triggered via hotkey!")
			ui.ShowCommandBar()
		}

		// Check for Ctrl + Escape (Kill Switch for rogue automation)
		if event.Message == types.WM_KEYDOWN && event.VKCode == types.VK_ESCAPE && ctrlDown {
			log.Println("🚨 KILL SWITCH ACTIVATED — Halting current operations.")
			if cancelFunc != nil {
				cancelFunc()
			}
		}
	}
}
