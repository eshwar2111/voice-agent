package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type OpenWebsiteTool struct{}

func (o *OpenWebsiteTool) Name() string {
	return "open_website"
}

func (o *OpenWebsiteTool) Description() string {
	return "Opens a specific URL in the default web browser."
}

func (o *OpenWebsiteTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"url": { "type": "string", "description": "The full HTTP/HTTPS URL to open." }
		},
		"required": ["url"]
	}`
}

func (o *OpenWebsiteTool) RequiresConfirmation() bool {
	return false
}

type OpenWebsiteArgs struct {
	URL string `json:"url"`
}

func (o *OpenWebsiteTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params OpenWebsiteArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	websiteURL := params.URL
	if strings.TrimSpace(websiteURL) == "" {
		return "", errors.New("missing url parameter")
	}

	fmt.Printf("Opening browser to: %s\n", websiteURL)
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", websiteURL)
	err := cmd.Start()
	if err != nil {
		return "", err
	}
	return "Website opened", nil
}
