package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type WindowsSettingsArgs struct {
	Setting string `json:"setting"` // "brightness", "bluetooth"
	Action  string `json:"action"`  // "set", "enable", "disable"
	Value   int    `json:"value"`   // Brightness level 0-100
}

type WindowsSettingsTool struct{}

func (t *WindowsSettingsTool) Name() string {
	return "windows_settings"
}

func (t *WindowsSettingsTool) Description() string {
	return "Adjusts OS-level hardware settings like screen brightness or Bluetooth."
}

func (t *WindowsSettingsTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"setting": { "type": "string", "enum": ["brightness", "bluetooth"], "description": "The setting to change" },
			"action": { "type": "string", "enum": ["set", "enable", "disable"], "description": "Action to perform" },
			"value": { "type": "integer", "description": "Level (0-100) for brightness" }
		},
		"required": ["setting", "action"]
	}`
}

func (t *WindowsSettingsTool) RequiresConfirmation() bool {
	return true
}

func (t *WindowsSettingsTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params WindowsSettingsArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	switch params.Setting {
	case "brightness":
		if params.Action != "set" {
			return "", fmt.Errorf("invalid action for brightness. Use 'set'")
		}
		if params.Value < 0 || params.Value > 100 {
			return "", fmt.Errorf("brightness value must be between 0 and 100")
		}
		psCmd := fmt.Sprintf(`(Get-WmiObject -Namespace root/WMI -Class WmiMonitorBrightnessMethods).WmiSetBrightness(1,%d)`, params.Value)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psCmd)
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("failed to set brightness: %w", err)
		}
		return fmt.Sprintf("Screen brightness set to %d%%.", params.Value), nil

	case "bluetooth":
		var psCmd string
		if params.Action == "enable" {
			psCmd = `Start-Process powershell -Verb RunAs -ArgumentList "-NoProfile -Command Get-PnpDevice -Class Bluetooth | Enable-PnpDevice -Confirm:$false"`
		} else if params.Action == "disable" {
			psCmd = `Start-Process powershell -Verb RunAs -ArgumentList "-NoProfile -Command Get-PnpDevice -Class Bluetooth | Disable-PnpDevice -Confirm:$false"`
		} else {
			return "", fmt.Errorf("invalid action for bluetooth. Use 'enable' or 'disable'")
		}
		
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psCmd)
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("failed to modify bluetooth: %w", err)
		}
		return fmt.Sprintf("Requested Bluetooth to be %sd. (May require UAC prompt)", params.Action), nil

	default:
		return "", fmt.Errorf("unknown setting: %s", params.Setting)
	}
}
