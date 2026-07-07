package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yourname/voice-agent/internal/ui"
)

type TimerArgs struct {
	DurationMinutes int    `json:"duration_minutes"`
	Message         string `json:"message"`
}

type TimerTool struct{}

func (t *TimerTool) Name() string {
	return "start_timer"
}

func (t *TimerTool) Description() string {
	return "Starts a background timer for the specified duration and shows an alert when finished."
}

func (t *TimerTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"duration_minutes": { "type": "integer", "description": "Duration in minutes" },
			"message": { "type": "string", "description": "Message to display when timer finishes" }
		},
		"required": ["duration_minutes"]
	}`
}

func (t *TimerTool) RequiresConfirmation() bool {
	return false
}

func (t *TimerTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params TimerArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	if params.DurationMinutes <= 0 {
		return "", fmt.Errorf("duration_minutes must be greater than 0")
	}

	msg := params.Message
	if msg == "" {
		msg = "Timer finished!"
	}

	go func(duration int, message string) {
		time.Sleep(time.Duration(duration) * time.Minute)
		ui.ShowOutputOverlay(fmt.Sprintf("⏰ Timer Complete: %s", message))
	}(params.DurationMinutes, msg)

	return fmt.Sprintf("Started a timer for %d minute(s).", params.DurationMinutes), nil
}
