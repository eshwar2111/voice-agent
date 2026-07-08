package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type SystemControlTool struct{}

func (t *SystemControlTool) Name() string             { return "system_control" }
func (t *SystemControlTool) Description() string       { return "System actions: lock, sleep, brightness up/down." }
func (t *SystemControlTool) RequiresConfirmation() bool { return false }
func (t *SystemControlTool) Parameters() string {
	return `{"type":"object","properties":{"action":{"type":"string","enum":["lock","sleep","brightness_up","brightness_down"]}},"required":["action"]}`
}

type systemArgs struct {
	Action string `json:"action"`
}

func (t *SystemControlTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a systemArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	var cmd *exec.Cmd
	switch a.Action {
	case "lock":
		cmd = exec.Command("rundll32", "user32.dll,LockWorkStation")
	case "sleep":
		cmd = exec.Command("rundll32", "powrprof.dll,SetSuspendState", "0,1,0")
	case "brightness_up":
		cmd = brightnessCmd("+10")
	case "brightness_down":
		cmd = brightnessCmd("-10")
	default:
		return "", fmt.Errorf("unknown system action: %q", a.Action)
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	return fmt.Sprintf("system: %s", a.Action), nil
}

// brightnessCmd adjusts brightness by a signed delta via WMI.
func brightnessCmd(delta string) *exec.Cmd {
	ps := fmt.Sprintf(
		`$b=(Get-WmiObject -Namespace root/WMI -Class WmiMonitorBrightness).CurrentBrightness; `+
			`$n=[Math]::Max(0,[Math]::Min(100,$b+(%s))); `+
			`(Get-WmiObject -Namespace root/WMI -Class WmiMonitorBrightnessMethods).WmiSetBrightness(1,$n)`,
		delta,
	)
	return exec.Command("powershell", "-NoProfile", "-Command", ps)
}
