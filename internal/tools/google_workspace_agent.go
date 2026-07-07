package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"github.com/yourname/voice-agent/internal/llm"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type GoogleWorkspaceAssistantTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *GoogleWorkspaceAssistantTool) Name() string { return "google_workspace_assistant" }

func (t *GoogleWorkspaceAssistantTool) Description() string {
	return "Specialized Google Workspace assistant for Gmail, Drive, Docs, Sheets, Slides, and Calendar. It turns natural-language requests into focused workspace actions and summaries."
}

func (t *GoogleWorkspaceAssistantTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"request": {"type": "string", "description": "Natural language Google Workspace request"},
			"mode": {"type": "string", "description": "Optional mode override: brief, compose_email, create_doc, create_sheet_report, draft_presentation, search_drive, search_docs, search_sheets, search_slides"}
		},
		"required": ["request"]
	}`
}

func (t *GoogleWorkspaceAssistantTool) RequiresConfirmation() bool { return false }

func (t *GoogleWorkspaceAssistantTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Request string `json:"request"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Request) == "" {
		return "", fmt.Errorf("request is required")
	}

	mode := strings.TrimSpace(strings.ToLower(args.Mode))
	if mode == "" {
		plannedMode, err := t.planMode(ctx, args.Request)
		if err != nil {
			mode = "brief"
		} else {
			mode = plannedMode
		}
	}

	switch mode {
	case "brief":
		return t.workspaceBrief(ctx)
	case "compose_email":
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		return ai.composeEmail(ctx, args.Request, nil)
	case "create_doc":
		client, err := auth.GetGoogleClient(ctx, t.Cfg)
		if err != nil {
			return "", err
		}
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		return ai.createDoc(ctx, client, args.Request, nil)
	case "create_sheet_report":
		client, err := auth.GetGoogleClient(ctx, t.Cfg)
		if err != nil {
			return "", err
		}
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		return ai.createSheetReport(ctx, client, args.Request, nil)
	case "draft_presentation":
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		return ai.draftPresentation(ctx, args.Request)
	case "search_drive":
		return (&GoogleDriveListTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"query": args.Request, "maxResults": 8}))
	case "search_docs":
		return (&GoogleDocsSearchTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"query": args.Request, "max_results": 8}))
	case "search_sheets":
		return (&GoogleSheetsSearchTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"query": args.Request, "max_results": 8}))
	case "search_slides":
		return (&GoogleSlidesSearchTool{Cfg: t.Cfg}).Execute(ctx, mustJSON(map[string]any{"query": args.Request, "max_results": 8}))
	default:
		return t.workspaceBrief(ctx)
	}
}

func (t *GoogleWorkspaceAssistantTool) planMode(ctx context.Context, request string) (string, error) {
	prompt := fmt.Sprintf(`Classify this Google Workspace request into exactly one mode.

Request: %s

Allowed modes:
- brief
- compose_email
- create_doc
- create_sheet_report
- draft_presentation
- search_drive
- search_docs
- search_sheets
- search_slides

Return ONLY JSON like {"mode":"brief"}`, request)

	response, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", err
	}

	var planned struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(extractJSONText(response)), &planned); err != nil {
		return "", err
	}
	if planned.Mode == "" {
		return "", fmt.Errorf("empty mode")
	}
	return strings.ToLower(planned.Mode), nil
}

func (t *GoogleWorkspaceAssistantTool) workspaceBrief(ctx context.Context) (string, error) {
	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	gmailSvc, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to create Gmail client: %w", err)
	}
	calendarSvc, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to create Calendar client: %w", err)
	}
	driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to create Drive client: %w", err)
	}

	unread, _ := gmailSvc.Users.Messages.List("me").Q("is:unread category:primary").MaxResults(5).Do()
	events, _ := calendarSvc.Events.List("primary").SingleEvents(true).TimeMin("now").OrderBy("startTime").MaxResults(5).Do()
	files, _ := driveSvc.Files.List().
		PageSize(5).
		OrderBy("modifiedTime desc").
		Fields("files(id,name,mimeType,modifiedTime)").
		Do()

	var sb strings.Builder
	sb.WriteString("Google Workspace Brief\n\n")

	if unread != nil {
		sb.WriteString(fmt.Sprintf("Inbox: %d unread primary message(s)\n", len(unread.Messages)))
		for i, msg := range unread.Messages {
			if i >= 3 {
				break
			}
			sb.WriteString(fmt.Sprintf("- Unread message ID: %s\n", msg.Id))
		}
	} else {
		sb.WriteString("Inbox: unavailable right now\n")
	}

	sb.WriteString("\nCalendar:\n")
	if events != nil && len(events.Items) > 0 {
		for i, event := range events.Items {
			if i >= 3 {
				break
			}
			when := event.Start.DateTime
			if when == "" {
				when = event.Start.Date
			}
			sb.WriteString(fmt.Sprintf("- %s at %s\n", fallbackText(event.Summary, "Untitled event"), when))
		}
	} else {
		sb.WriteString("- No upcoming events found\n")
	}

	sb.WriteString("\nRecent Drive Activity:\n")
	if files != nil && len(files.Files) > 0 {
		for i, file := range files.Files {
			if i >= 4 {
				break
			}
			sb.WriteString(fmt.Sprintf("- %s (%s)\n", file.Name, workspaceMimeLabel(file.MimeType)))
		}
	} else {
		sb.WriteString("- No recent files found\n")
	}

	sb.WriteString("\nCapabilities: Gmail, Drive, Docs, Sheets, Slides, Calendar")
	return sb.String(), nil
}

func workspaceMimeLabel(mimeType string) string {
	switch mimeType {
	case "application/vnd.google-apps.document":
		return "Google Doc"
	case "application/vnd.google-apps.spreadsheet":
		return "Google Sheet"
	case "application/vnd.google-apps.presentation":
		return "Google Slides"
	case "application/vnd.google-apps.folder":
		return "Folder"
	default:
		return mimeType
	}
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
