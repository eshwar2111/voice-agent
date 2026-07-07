package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

type MicrosoftCalendarListTool struct {
	Cfg *config.Config
}

func (t *MicrosoftCalendarListTool) Name() string {
	return "microsoft_calendar_list"
}

func (t *MicrosoftCalendarListTool) Description() string {
	return "List upcoming events from the user's Microsoft Calendar."
}

func (t *MicrosoftCalendarListTool) Parameters() string {
	return `{"type": "object", "properties": {"top": {"type": "integer", "description": "Number of events to fetch"}}, "required": []}`
}

func (t *MicrosoftCalendarListTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Top int `json:"top"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if args.Top <= 0 {
		args.Top = 10
	}

	client, err := auth.GetMicrosoftClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	start := time.Now().Format(time.RFC3339)
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/calendarview?startDateTime=%s&endDateTime=%s&$top=%d",
		start, time.Now().Add(7*24*time.Hour).Format(time.RFC3339), args.Top)

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to call Graph API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Graph API error: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Value []struct {
			Subject string `json:"subject"`
			Start   struct {
				DateTime string `json:"dateTime"`
			} `json:"start"`
		} `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode Graph API response: %w", err)
	}

	if len(result.Value) == 0 {
		return `{"type":"calendar_list","data":[]}`, nil
	}

	type MSFTEventInfo struct {
		ID        string `json:"id"`
		Summary   string `json:"summary"`
		StartTime string `json:"startTime"`
		EndTime   string `json:"endTime"`
		Location  string `json:"location,omitempty"`
		JoinLink  string `json:"joinLink,omitempty"`
	}

	var parsedEvents []MSFTEventInfo
	for _, event := range result.Value {
		parsedEvents = append(parsedEvents, MSFTEventInfo{
			Summary:   event.Subject,
			StartTime: event.Start.DateTime,
		})
	}

	b, err := json.Marshal(map[string]interface{}{
		"type": "calendar_list",
		"data": parsedEvents,
	})
	return string(b), err
}

func (t *MicrosoftCalendarListTool) RequiresConfirmation() bool {
	return false
}
