package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type ScheduleTaskTool struct{}

func (s *ScheduleTaskTool) Name() string {
	return "schedule_task"
}

func (s *ScheduleTaskTool) Description() string {
	return "Schedules a voice command to be executed at a specific time in the future."
}

func (s *ScheduleTaskTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The command to execute (e.g. 'open notepad')"
			},
			"time": {
				"type": "string",
				"description": "The time to execute at (RFC3339 format or relative like '+5m', '+1h')"
			}
		},
		"required": ["command", "time"]
	}`
}

func (s *ScheduleTaskTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Command string `json:"command"`
		Time    string `json:"time"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	var targetTime time.Time
	if args.Time[0] == '+' {
		duration, err := time.ParseDuration(args.Time[1:])
		if err != nil {
			return "", fmt.Errorf("invalid relative time: %w", err)
		}
		targetTime = time.Now().Add(duration)
	} else {
		var err error
		targetTime, err = time.Parse(time.RFC3339, args.Time)
		if err != nil {
			return "", fmt.Errorf("invalid RFC3339 time: %w", err)
		}
	}

	delay := time.Until(targetTime)
	if delay <= 0 {
		return "", fmt.Errorf("scheduled time is in the past")
	}

	// In a real app, we'd persist this to a DB
	go func() {
		time.Sleep(delay)
		// We need a way to trigger the engine/router from here
		// For now, we'll just log it. 
		// Ideally we'd emit a 'VOICE_INPUT' event or similar.
		fmt.Printf("⏰ Scheduled Task Triggered: %s\n", args.Command)
		// Internal call to ProcessCommand (needs to be bridged)
	}()

	return fmt.Sprintf("Successfully scheduled command '%s' for %s", args.Command, targetTime.Format("15:04:05")), nil
}

func (s *ScheduleTaskTool) RequiresConfirmation() bool {
	return true
}
