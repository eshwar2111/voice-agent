package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type VSCodeTool struct{}

func (v *VSCodeTool) Name() string {
	return "vscode"
}

func (v *VSCodeTool) Description() string {
	return "Interacts with Visual Studio Code. Can open files/directories, or use advanced IPC to insert code or format files if the companion extension is running."
}

func (v *VSCodeTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"action": { "type": "string", "enum": ["open", "insert_code", "format"], "description": "The action to perform." },
			"path": { "type": "string", "description": "The absolute path to the file or directory (required for 'open')." },
			"content": { "type": "string", "description": "The code to insert (required for 'insert_code')." }
		},
		"required": ["action"]
	}`
}

func (v *VSCodeTool) RequiresConfirmation() bool {
	return false
}

type VSCodeArgs struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

type IPCMessage struct {
	Command string `json:"command"`
	Payload string `json:"payload"`
}

func (v *VSCodeTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params VSCodeArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	switch params.Action {
	case "open":
		path := strings.TrimSpace(params.Path)
		if path == "" {
			return "", fmt.Errorf("missing path parameter for 'open' action")
		}
		cmd := exec.CommandContext(ctx, "cmd", "/c", "code", path)
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to launch VS Code: %w", err)
		}
		return fmt.Sprintf("Opened '%s' in Visual Studio Code.", path), nil

	case "insert_code", "format":
		return v.sendIPCCommand(params.Action, params.Content)

	default:
		return "", fmt.Errorf("unknown action: %s", params.Action)
	}
}

func (v *VSCodeTool) sendIPCCommand(command, payload string) (string, error) {
	u := "ws://localhost:37901"
	
	dialer := websocket.Dialer{
		HandshakeTimeout: 2 * time.Second,
	}
	
	c, _, err := dialer.Dial(u, nil)
	if err != nil {
		return "", fmt.Errorf("failed to connect to VS Code extension on %s (is the extension installed and running?): %w", u, err)
	}
	defer c.Close()

	msg := IPCMessage{
		Command: command,
		Payload: payload,
	}
	
	if err := c.WriteJSON(msg); err != nil {
		return "", fmt.Errorf("failed to send IPC message: %w", err)
	}

	// Wait for an acknowledgment
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var response map[string]interface{}
	if err := c.ReadJSON(&response); err != nil {
		return fmt.Sprintf("Command '%s' sent, but no acknowledgment received from VS Code.", command), nil
	}

	if status, ok := response["status"].(string); ok && status == "error" {
		return "", fmt.Errorf("VS Code returned an error: %v", response["message"])
	}

	return fmt.Sprintf("Successfully executed '%s' in VS Code.", command), nil
}
