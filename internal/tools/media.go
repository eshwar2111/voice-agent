package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type MediaArgs struct {
	Query string `json:"query"`
}

type MediaPlayerTool struct{}

func (t *MediaPlayerTool) Name() string {
	return "play_music"
}

func (t *MediaPlayerTool) Description() string {
	return "Plays a specific song or artist. It uses Spotify if available, or falls back to system defaults."
}

func (t *MediaPlayerTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": { "type": "string", "description": "The song name, artist, or playlist to search and play" }
		},
		"required": ["query"]
	}`
}

func (t *MediaPlayerTool) RequiresConfirmation() bool {
	return false
}

func (t *MediaPlayerTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params MediaArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	query := strings.TrimSpace(params.Query)
	if query == "" {
		return "", fmt.Errorf("missing query parameter")
	}

	spotifyURI := "spotify:search:" + url.QueryEscape(query)
	
	cmd := exec.CommandContext(ctx, "cmd", "/C", "start", spotifyURI)
	err := cmd.Start()
	if err != nil {
		youtubeURL := "https://www.youtube.com/results?search_query=" + url.QueryEscape(query)
		exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", youtubeURL).Start()
		return fmt.Sprintf("Could not open Spotify. Opened YouTube search for '%s' instead.", query), nil
	}

	return fmt.Sprintf("Sent request to play '%s' on Spotify.", query), nil
}
