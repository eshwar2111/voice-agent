package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/internal/automation"
)

type NativeClickTool struct{}

func (t *NativeClickTool) Name() string {
	return "native_click"
}

func (t *NativeClickTool) Description() string {
	return "Programmatically clicks a native Windows element using UIAutomation by finding its Name."
}

func (t *NativeClickTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"element_name": {
				"type": "string",
				"description": "The name or automation ID of the element to click."
			}
		},
		"required": ["element_name"]
	}`
}

func (t *NativeClickTool) RequiresConfirmation() bool {
	return false
}

func (t *NativeClickTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		ElementName string `json:"element_name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	err := automation.ClickElementByName(params.ElementName)
	if err != nil {
		return "", fmt.Errorf("native click failed: %w", err)
	}

	return fmt.Sprintf("Successfully clicked element: %s", params.ElementName), nil
}
