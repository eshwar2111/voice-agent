package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type GoogleCalendarListTool struct {
	Cfg *config.Config
}

func (t *GoogleCalendarListTool) Name() string {
	return "google_calendar_list"
}

func (t *GoogleCalendarListTool) Description() string {
	return "List upcoming events from the user's Google Calendar. Default is 10 events."
}

func (t *GoogleCalendarListTool) Parameters() string {
	return `{"type": "object", "properties": {"maxResults": {"type": "integer", "description": "Maximum number of events to list"}}, "required": []}`
}

func (t *GoogleCalendarListTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		MaxResults int64 `json:"maxResults"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 10
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	srv, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Calendar client: %w", err)
	}

	t_now := time.Now().Format(time.RFC3339)
	events, err := srv.Events.List("primary").ShowDeleted(false).
		SingleEvents(true).TimeMin(t_now).MaxResults(args.MaxResults).OrderBy("startTime").Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve next 10 of the user's events: %w", err)
	}

	if len(events.Items) == 0 {
		return ToolResult{
			Summary: "No upcoming primary calendar events found.",
			Artifacts: map[string]interface{}{
				"type": "calendar_list",
				"data": []interface{}{},
			},
		}.String(), nil
	}

	type EventInfo struct {
		ID        string `json:"id"`
		Summary   string `json:"summary"`
		StartTime string `json:"startTime"`
		EndTime   string `json:"endTime"`
		Location  string `json:"location,omitempty"`
		JoinLink  string `json:"joinLink,omitempty"`
	}

	var parsedEvents []EventInfo
	for _, item := range events.Items {
		date := item.Start.DateTime
		if date == "" {
			date = item.Start.Date
		}
		end := item.End.DateTime
		if end == "" {
			end = item.End.Date
		}

		parsedEvents = append(parsedEvents, EventInfo{
			ID:        item.Id,
			Summary:   item.Summary,
			StartTime: date,
			EndTime:   end,
			Location:  item.Location,
			JoinLink:  item.HangoutLink,
		})
	}

	return ToolResult{
		Summary: fmt.Sprintf("Found %d upcoming calendar event(s).", len(parsedEvents)),
		Artifacts: map[string]interface{}{
			"type": "calendar_list",
			"data": parsedEvents,
		},
	}.String(), nil
}

func (t *GoogleCalendarListTool) RequiresConfirmation() bool {
	return false
}

type GoogleCalendarAddEventTool struct {
	Cfg *config.Config
}

func (t *GoogleCalendarAddEventTool) Name() string {
	return "google_calendar_add_event"
}

func (t *GoogleCalendarAddEventTool) Description() string {
	return "Add a new event to the user's Google Calendar."
}

func (t *GoogleCalendarAddEventTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"summary": {"type": "string", "description": "Event summary/title"},
			"description": {"type": "string", "description": "Event description"},
			"startTime": {"type": "string", "description": "Start time in RFC3339 format (e.g. 2026-03-24T10:00:00Z)"},
			"endTime": {"type": "string", "description": "End time in RFC3339 format (e.g. 2026-03-24T11:00:00Z)"},
			"location": {"type": "string", "description": "Event location"},
			"attendees": {"type": "array", "items": {"type": "string"}, "description": "List of attendee email addresses to invite"}
		},
		"required": ["summary", "startTime", "endTime"]
	}`
}

func (t *GoogleCalendarAddEventTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Summary     string   `json:"summary"`
		Description string   `json:"description"`
		StartTime   string   `json:"startTime"`
		EndTime     string   `json:"endTime"`
		Location    string   `json:"location"`
		Attendees   []string `json:"attendees"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	srv, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Calendar client: %w", err)
	}

	var attendeeList []*calendar.EventAttendee
	for _, a := range args.Attendees {
		attendeeList = append(attendeeList, &calendar.EventAttendee{Email: a})
	}

	event := &calendar.Event{
		Summary:     args.Summary,
		Location:    args.Location,
		Description: args.Description,
		Start: &calendar.EventDateTime{
			DateTime: args.StartTime,
			TimeZone: "UTC",
		},
		End: &calendar.EventDateTime{
			DateTime: args.EndTime,
			TimeZone: "UTC",
		},
		Attendees: attendeeList,
	}

	event, err = srv.Events.Insert("primary", event).Do()
	if err != nil {
		return "", fmt.Errorf("unable to create event: %w", err)
	}

	return ToolResult{
		Summary: fmt.Sprintf("✅ **Event Created Successfully!**\n\n[View on Google Calendar](%s)", event.HtmlLink),
		Artifacts: map[string]interface{}{
			"id":        event.Id,
			"summary":   args.Summary,
			"htmlLink":  event.HtmlLink,
			"startTime": args.StartTime,
			"endTime":   args.EndTime,
		},
	}.String(), nil
}

func (t *GoogleCalendarAddEventTool) RequiresConfirmation() bool {
	return true
}

type GoogleCalendarSearchTool struct {
	Cfg *config.Config
}

func (t *GoogleCalendarSearchTool) Name() string {
	return "google_calendar_search"
}

func (t *GoogleCalendarSearchTool) Description() string {
	return "Search for events in the user's Google Calendar using a query string."
}

func (t *GoogleCalendarSearchTool) Parameters() string {
	return `{"type": "object", "properties": {"query": {"type": "string", "description": "Search query for events"}, "maxResults": {"type": "integer", "description": "Maximum number of events to list"}}, "required": ["query"]}`
}

func (t *GoogleCalendarSearchTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int64  `json:"maxResults"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 10
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	srv, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Calendar client: %w", err)
	}

	events, err := srv.Events.List("primary").Q(args.Query).MaxResults(args.MaxResults).Do()
	if err != nil {
		return "", fmt.Errorf("unable to search events: %w", err)
	}

	if len(events.Items) == 0 {
		return fmt.Sprintf("No events found matching query: %s", args.Query), nil
	}

	result := fmt.Sprintf("Calendar search results for '%s':\n", args.Query)
	var parsedEvents []map[string]string
	for _, item := range events.Items {
		date := item.Start.DateTime
		if date == "" {
			date = item.Start.Date
		}
		result += fmt.Sprintf("- %s (%s)\n", item.Summary, date)
		parsedEvents = append(parsedEvents, map[string]string{
			"id":      item.Id,
			"summary": item.Summary,
			"start":   date,
			"link":    item.HtmlLink,
		})
	}

	return ToolResult{
		Summary: result,
		Artifacts: map[string]interface{}{
			"type":  "calendar_search",
			"query": args.Query,
			"data":  parsedEvents,
		},
	}.String(), nil
}

func (t *GoogleCalendarSearchTool) RequiresConfirmation() bool {
	return false
}
