package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

type SpotifySaveTrackTool struct {
	Cfg *config.Config
}

func (t *SpotifySaveTrackTool) Name() string { return "spotify_save_track" }
func (t *SpotifySaveTrackTool) Description() string {
	return "Save (like), remove, or check a track in your Spotify Liked Songs. Defaults to the currently playing track."
}
func (t *SpotifySaveTrackTool) Parameters() string {
	return `{"type": "object", "properties": {"action": {"type": "string", "description": "save|remove|check (default save)"}, "track_id": {"type": "string", "description": "optional; defaults to current track"}}, "required": []}`
}
func (t *SpotifySaveTrackTool) RequiresConfirmation() bool { return false }

func (t *SpotifySaveTrackTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Action  string `json:"action"`
		TrackID string `json:"track_id"`
	}
	json.Unmarshal(params, &args)

	action := strings.TrimSpace(strings.ToLower(args.Action))
	if action == "" {
		action = "save"
	}

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	id := strings.TrimSpace(args.TrackID)
	if id == "" {
		body, gerr := spotifyGet(ctx, client, "/me/player/currently-playing")
		if gerr != nil {
			return "", gerr
		}
		if body == nil {
			return "Nothing is currently playing — start a song or pass a track_id.", nil
		}
		var now struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		}
		if json.Unmarshal(body, &now) != nil || now.Item.ID == "" {
			return "Nothing is currently playing — start a song or pass a track_id.", nil
		}
		id = now.Item.ID
	}

	switch action {
	case "save":
		_, err = spotifyPut(ctx, client, "/me/tracks?ids="+id, nil)
		if msg, handled := spotifyScopeError(err); handled {
			return msg, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed to save track: %w", err)
		}
		return "Saved to your Liked Songs", nil

	case "remove":
		_, err = spotifyDelete(ctx, client, "/me/tracks?ids="+id)
		if msg, handled := spotifyScopeError(err); handled {
			return msg, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed to remove track: %w", err)
		}
		return "Removed from Liked Songs", nil

	case "check":
		body, cerr := spotifyGet(ctx, client, "/me/tracks/contains?ids="+id)
		if msg, handled := spotifyScopeError(cerr); handled {
			return msg, nil
		}
		if cerr != nil {
			return "", fmt.Errorf("failed to check track: %w", cerr)
		}
		var contains []bool
		json.Unmarshal(body, &contains)
		if len(contains) > 0 && contains[0] {
			return "That song is saved", nil
		}
		return "Not saved yet", nil
	}

	return fmt.Sprintf("Unknown action: %s. Supported: save, remove, check", action), nil
}

// spotifyScopeError maps a missing-scope 403 to a friendly re-link prompt.
func spotifyScopeError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	msg := err.Error()
	// Match the HTTP status portion "(403 " (helpers format errors as
	// "Spotify error (403 Forbidden): <body>") or Spotify's scope message, so a
	// body that merely contains the digits 403 doesn't trigger a false re-link.
	if strings.Contains(msg, "(403 ") || strings.Contains(msg, "Insufficient client scope") {
		return "Re-link Spotify to enable saving songs (⚙ → Spotify).", true
	}
	return "", false
}
