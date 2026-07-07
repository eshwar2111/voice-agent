package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type SystemStatusTool struct{}

func (s *SystemStatusTool) Name() string {
	return "system_status"
}

func (s *SystemStatusTool) Description() string {
	return "Gets the current system status including CPU usage, available memory, and active processes."
}

func (s *SystemStatusTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {},
		"required": []
	}`
}

func (s *SystemStatusTool) RequiresConfirmation() bool {
	return false
}

func (s *SystemStatusTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	// Execute PowerShell command to get basic stats
	psScript := `
		$mem = Get-CimInstance Win32_OperatingSystem | Select-Object FreePhysicalMemory, TotalVisibleMemorySize
		$cpu = Get-CimInstance Win32_Processor | Measure-Object -Property LoadPercentage -Average | Select-Object Average
		$freeMemGB = [math]::Round($mem.FreePhysicalMemory / 1048576, 2)
		$totalMemGB = [math]::Round($mem.TotalVisibleMemorySize / 1048576, 2)
		
		$obj = @{
			type = "system_status"
			data = @{
				cpu = $cpu.Average
				ramFree = $freeMemGB
				ramTotal = $totalMemGB
			}
		}
		$obj | ConvertTo-Json -Compress
	`

	cmd := exec.Command("powershell", "-NoProfile", "-Command", psScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get system status: %v", err)
	}

	return strings.TrimSpace(string(out)), nil
}
