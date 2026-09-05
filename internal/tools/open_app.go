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
	"github.com/yourname/voice-agent/internal/ui"
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
	cands := executor.FindAppCandidates(appName)

	var app executor.StartApp
	switch {
	case len(cands) == 1:
		app = cands[0]
	case len(cands) >= 2:
		// Ask-don't-guess: several installed apps match the spoken name.
		shown := cands
		if len(shown) > 5 {
			shown = shown[:5]
		}
		opts := make([]ui.Option, 0, len(shown))
		for _, c := range shown {
			opts = append(opts, ui.Option{ID: c.AppID, Label: c.Name})
		}
		id, ok := ui.AskChoice(fmt.Sprintf("Which app for %q?", appName), opts)
		if !ok {
			return "Cancelled", nil // user backed out — don't launch the wrong app
		}
		for _, c := range cands {
			if c.AppID == id {
				app = c
				break
			}
		}
	}

	if app.AppID != "" {
		fmt.Printf("Opening: %s (AppID: %s)\n", app.Name, app.AppID)
		cmd := exec.Command("explorer.exe", `shell:AppsFolder\`+app.AppID)
		if err := cmd.Start(); err != nil {
			return "", err
		}
		return fmt.Sprintf("Opened %s", app.Name), nil
	}

	// No installed-app match — fall back to a basic shell start by name.
	fmt.Printf("No installed match for %s; falling back to shell start...\n", appName)
	cmd := exec.Command("cmd", "/C", "start", appName)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	return "Application launched via fallback successfully", nil
}
