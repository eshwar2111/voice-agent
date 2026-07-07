package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/yourname/voice-agent/internal/security"
)

type TerminalArgs struct {
	Command string `json:"command"`
}

type TerminalTool struct{}

func (t *TerminalTool) Name() string {
	return "run_terminal_command"
}

func (t *TerminalTool) Description() string {
	return "Executes a CLI command in the terminal. Useful for running git, npm, python, or basic system commands."
}

func (t *TerminalTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"command": { "type": "string", "description": "The command to execute (e.g. 'git status')" }
		},
		"required": ["command"]
	}`
}

func (t *TerminalTool) RequiresConfirmation() bool {
	return false // We handle dynamic confirmation internally
}

func isSafeCommand(cmd string) bool {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}
	base := parts[0]

	// Whitelist of strictly safe read-only or harmless commands
	whitelist := map[string]bool{
		"dir":  true,
		"cd":   true,
		"echo": true,
		"ping": true,
		"cls":  true,
	}

	if whitelist[base] {
		if strings.Contains(cmd, "&&") || strings.Contains(cmd, ";") || strings.Contains(cmd, "|") {
			return false
		}
		return true
	}
	
	if base == "git" {
		if len(parts) > 1 {
			subcmd := parts[1]
			safeGit := map[string]bool{
				"status": true, "log": true, "diff": true, "show": true, "branch": true,
			}
			if safeGit[subcmd] {
				return true
			}
		}
	}

	return false
}

func isBlacklisted(cmd string) bool {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	
	blacklist := []string{
		"format", "del /f /s", "rmdir /s", "shutdown", "restart", "diskpart",
	}

	for _, b := range blacklist {
		if strings.Contains(cmd, b) {
			return true
		}
	}
	return false
}

func (t *TerminalTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params TerminalArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	cmdStr := strings.TrimSpace(params.Command)
	if cmdStr == "" {
		return "", fmt.Errorf("missing command parameter")
	}

	if isBlacklisted(cmdStr) {
		return "", fmt.Errorf("command is completely blocked for security reasons")
	}

	if !isSafeCommand(cmdStr) {
		// Medium or high risk command - requires confirmation
		approved := security.RequestConfirmation(t.Name(), rawParams)
		if !approved {
			return "", fmt.Errorf("user denied execution of command: %s", cmdStr)
		}
	}

	cmd := exec.CommandContext(ctx, "cmd", "/C", cmdStr)
	out, err := cmd.CombinedOutput()
	
	outputStr := string(out)
	if err != nil {
		return fmt.Sprintf("❌ **Command Failed**\n\n**Error:** %v\n\n**Output:**\n```\n%s\n```", err, string(out)), nil
	}

	if outputStr == "" {
		return "✅ **Command Executed Successfully**\n\n(No output)", nil
	}
	
	if len(outputStr) > 4000 {
		outputStr = outputStr[:4000] + "\n...[truncated]"
	}

	return fmt.Sprintf("✅ **Command Executed**\n\n**Output:**\n```\n%s\n```", outputStr), nil
}
