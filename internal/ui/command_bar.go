package ui

import (
	"log"
)

func ShowCommandBar() {
	// Call the new WebView-based command bar in overlay.go
	// This avoids the lxn/walk threading issues and matches the Dynamic Island theme
	ShowCommandBarInOverlay()
}

var OnCommand func(string)
var OnHistoryUp func() string
var OnHistoryDown func() string

func ProcessCommand(input string) {
	if OnCommand != nil {
		OnCommand(input)
	} else {
		log.Printf("Command Received (no handler): %s", input)
	}
}
