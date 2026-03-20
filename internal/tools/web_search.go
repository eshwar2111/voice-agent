package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type WebSearchTool struct{}

func (w *WebSearchTool) Name() string {
	return "web_search"
}

func (w *WebSearchTool) Description() string {
	return "Opens the default browser and performs a DuckDuckGo search for the specific query."
}

func (w *WebSearchTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": { "type": "string", "description": "The search term to query." }
		},
		"required": ["query"]
	}`
}

func (w *WebSearchTool) RequiresConfirmation() bool {
	return false
}

type WebSearchArgs struct {
	Query string `json:"query"`
}

func (w *WebSearchTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params WebSearchArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	query := params.Query
	if strings.TrimSpace(query) == "" {
		return "", errors.New("missing query parameter")
	}

	searchURL := "https://duckduckgo.com/?q=" + url.QueryEscape(query)
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", searchURL)
	err := cmd.Start()
	if err != nil {
		return "", err
	}
	return "Web search initiated", nil
}
