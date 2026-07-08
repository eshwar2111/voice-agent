package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-vgo/robotgo"
)

type MediaControlTool struct{}

func (t *MediaControlTool) Name() string        { return "media_control" }
func (t *MediaControlTool) Description() string { return "Controls system media playback and volume." }
func (t *MediaControlTool) RequiresConfirmation() bool { return false }
func (t *MediaControlTool) Parameters() string {
	return `{"type":"object","properties":{"action":{"type":"string","enum":["play","pause","next","previous","volume_up","volume_down","mute"]}},"required":["action"]}`
}

type mediaArgs struct {
	Action string `json:"action"`
}

// robotgo special keys for media/volume control (Windows).
var mediaKey = map[string]string{
	"play":        "audio_play",
	"pause":       "audio_play", // toggle
	"next":        "audio_next",
	"previous":    "audio_prev",
	"volume_up":   "audio_vol_up",
	"volume_down": "audio_vol_down",
	"mute":        "audio_mute",
}

func (t *MediaControlTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a mediaArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	key, ok := mediaKey[a.Action]
	if !ok {
		return "", fmt.Errorf("unknown media action: %q", a.Action)
	}
	robotgo.KeyTap(key)
	return fmt.Sprintf("media: %s", a.Action), nil
}
