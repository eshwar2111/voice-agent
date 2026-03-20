package security

import (
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/internal/ui"
)

// RequestConfirmation prompts the user for approval via the UI overlay before proceeding.
func RequestConfirmation(toolName string, params json.RawMessage) bool {
	msg := fmt.Sprintf("The agent wants to use %s with parameters: %s", toolName, string(params))
	fmt.Printf("⚠️  [SECURITY] Requesting UI confirmation for: %s\n", toolName)

	return ui.RequestConfirmation(msg)
}
