package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

type SpotifyTransferTool struct {
	Cfg *config.Config
}

func (t *SpotifyTransferTool) Name() string { return "spotify_transfer" }
func (t *SpotifyTransferTool) Description() string {
	return "Transfer Spotify playback to a specific device by name (e.g. 'Laptop', 'Kitchen Speaker')."
}
func (t *SpotifyTransferTool) Parameters() string {
	return `{"type": "object", "properties": {"device": {"type": "string", "description": "device name, e.g. 'Laptop'"}}, "required": ["device"]}`
}
func (t *SpotifyTransferTool) RequiresConfirmation() bool { return false }

func (t *SpotifyTransferTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Device string `json:"device"`
	}
	json.Unmarshal(params, &args)

	device := strings.TrimSpace(args.Device)

	client, err := auth.GetSpotifyClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	name, err := ensureActiveDevice(ctx, client, device)
	if err != nil {
		return "", err
	}

	return "Transferred playback to " + name, nil
}
