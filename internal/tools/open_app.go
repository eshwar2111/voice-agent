package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/yourname/voice-agent/internal/executor"
)

var safeAppRegex = regexp.MustCompile(`^[a-zA-Z0-9\s\-\.]+$`)

type OpenAppTool struct{}

func (o *OpenAppTool) Name() string {
	return "open_app"
}

func (o *OpenAppTool) Description() string {
	return "Opens an installed application"
}

func (o *OpenAppTool) Parameters() string {
	return `{"app_name": "string (required)"}`
}

func (o *OpenAppTool) RequiresConfirmation() bool {
	return false
}

type OpenAppArgs struct {
	AppName string `json:"app_name"`
}

func (o *OpenAppTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params OpenAppArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	appName := params.AppName
	if strings.TrimSpace(appName) == "" { // Modified check
		return "", errors.New("missing app_name parameter")
	}
	if !safeAppRegex.MatchString(appName) {
		return "", errors.New("app name contains unsafe characters, possible command injection")
	}

	fmt.Printf("Searching for installed app: %s...\n", appName)
	app, found := executor.FindApp(appName)
	if found {
		fmt.Printf("Found match: %s (AppID: %s)\n", app.Name, app.AppID)
		cmd := exec.Command("explorer.exe", `shell:AppsFolder\`+app.AppID)
		err := cmd.Start()
		if err != nil {
			return "", err
		}
		return "Application launched successfully", nil
	}

	fmt.Printf("Could not find exact match for %s, falling back to basic shell execution...\n", appName)
	cmd := exec.Command("cmd", "/C", "start", appName)
	err := cmd.Start()
	if err != nil {
		return "", err
	}

	return "Application launched via fallback successfully", nil
}
