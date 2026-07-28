package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type WebSearchTool struct{}

func (w *WebSearchTool) Name() string {
	return "web_search"
}

func (w *WebSearchTool) Description() string {
	return "Searches the web (DuckDuckGo) and returns the top results (title, URL, snippet) as text to reason over. For a deep synthesized answer use 'research' instead."
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
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return "", errors.New("missing query parameter")
	}
	results, err := ddgSearch(ctx, query, 6)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	if len(results) == 0 {
		return "No results found for: " + query, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Web results for %q:\n\n", query)
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return b.String(), nil
}
