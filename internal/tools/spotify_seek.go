package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

type SpotifySeekTool struct {
	Cfg *config.Config
}

func (t *SpotifySeekTool) Name() string { return "spotify_seek" }
func (t *SpotifySeekTool) Description() string {
	return "Seek within the current Spotify track. Accepts an absolute position (mm:ss or seconds) or a relative jump (+30s / -15s)."
}
func (t *SpotifySeekTool) Parameters() string {
	return `{"type": "object", "properties": {"position": {"type": "string", "description": "mm:ss, seconds, or relative +30s/-15s"}}, "required": ["position"]}`
}
func (t *SpotifySeekTool) RequiresConfirmation() bool { return false }

func (t *SpotifySeekTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Position string `json:"position"`
	}
	json.Unmarshal(params, &args)

	position := strings.TrimSpace(args.Position)
	if position == "" {
		return "", fmt.Errorf("position is required")
	}

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	currentMs := 0
	// Relative seeks need the current progress as a baseline.
	if position[0] == '+' || position[0] == '-' {
		body, gerr := spotifyGet(ctx, client, "/me/player/currently-playing")
		if gerr == nil && body != nil {
			var now struct {
				ProgressMS int `json:"progress_ms"`
			}
			if json.Unmarshal(body, &now) == nil {
				currentMs = now.ProgressMS
			}
		}
	}

	ms, err := parseSeekPosition(position, currentMs)
	if err != nil {
		return "", err
	}

	seek := func() error {
		_, e := spotifyPut(ctx, client, fmt.Sprintf("/me/player/seek?position_ms=%d", ms), nil)
		return e
	}
	err = seek()
	if isNoActiveDevice(err) {
		if _, derr := ensureActiveDevice(ctx, client, ""); derr != nil {
			return "", derr
		}
		err = seek()
	}
	if err != nil {
		return "", fmt.Errorf("failed to seek: %w", err)
	}

	secs := ms / 1000
	return fmt.Sprintf("Seeked to %d:%02d", secs/60, secs%60), nil
}
