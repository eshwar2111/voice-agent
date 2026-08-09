package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type GmailMessageInfo struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Date    string `json:"date,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	Unread  bool   `json:"unread"`
}

type GoogleGmailListTool struct {
	Cfg *config.Config
}

func (t *GoogleGmailListTool) Name() string {
	return "google_gmail_list"
}

func (t *GoogleGmailListTool) Description() string {
	return "List recent emails from the user's Gmail inbox. Returns JSON with from and subject."
}

func (t *GoogleGmailListTool) Parameters() string {
	return `{"type": "object", "properties": {"maxResults": {"type": "integer", "description": "Maximum number of emails to list"}}, "required": []}`
}

func (t *GoogleGmailListTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		MaxResults int64 `json:"maxResults"`
	}
	// Ignore unmarshal errors — params may be empty
	_ = json.Unmarshal(params, &args)
	if args.MaxResults <= 0 {
		args.MaxResults = 6
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Gmail client: %w", err)
	}

	// Filter to Primary category only — excludes Promotions, Social, Updates
	res, err := srv.Users.Messages.List("me").
		Q("category:primary").
		MaxResults(args.MaxResults).
		Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve messages: %w", err)
	}

	if len(res.Messages) == 0 {
		return "No primary inbox messages found.", nil
	}

	var messages []GmailMessageInfo
	for _, m := range res.Messages {
		// Fetch metadata + snippet in one call
		msg, err := srv.Users.Messages.Get("me", m.Id).
			Format("metadata").
			MetadataHeaders("Subject", "From", "Date").
			Do()
		if err != nil {
			continue
		}

		info := GmailMessageInfo{
			ID:      m.Id,
			Snippet: msg.Snippet,
		}

		// Check unread status
		for _, label := range msg.LabelIds {
			if label == "UNREAD" {
				info.Unread = true
				break
			}
		}

		for _, h := range msg.Payload.Headers {
			switch h.Name {
			case "Subject":
				info.Subject = h.Value
			case "From":
				// Clean: "John Doe <john@gmail.com>" → "John Doe"
				info.From = cleanSenderName(h.Value)
			case "Date":
				info.Date = formatEmailDate(h.Value)
			}
		}
		messages = append(messages, info)
	}

	return ToolResult{
		Summary: fmt.Sprintf("Found %d primary inbox message(s).", len(messages)),
		Artifacts: map[string]interface{}{
			"type": "gmail_list",
			"data": messages,
		},
	}.String(), nil
}

// cleanSenderName extracts just the display name from an RFC 5322 From header.
// e.g. "John Doe <john@example.com>" → "John Doe", "john@example.com" → "john@example.com"
func cleanSenderName(from string) string {
	if idx := strings.Index(from, " <"); idx > 0 {
		return strings.TrimSpace(from[:idx])
	}
	if idx := strings.Index(from, "<"); idx > 0 {
		return strings.TrimSpace(from[:idx])
	}
	return strings.TrimSpace(from)
}

// formatEmailDate turns a full RFC 2822 date into a short human form.
// e.g. "Thu, 26 Mar 2026 12:29:18 +0000 (UTC)" → "Mar 26"
func formatEmailDate(raw string) string {
	// Try several common formats
	formats := []string{
		"Mon, 2 Jan 2006 15:04:05 -0700 (MST)",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 +0000 (UTC)",
		"2 Jan 2006 15:04:05 -0700",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, raw); err == nil {
			return t.Format("Jan 2")
		}
	}
	// Fallback: return as-is trimmed
	if len(raw) > 16 {
		return raw[:16]
	}
	return raw
}

func (t *GoogleGmailListTool) RequiresConfirmation() bool {
	return false
}

type GoogleGmailSendTool struct {
	Cfg *config.Config
}

func (t *GoogleGmailSendTool) Name() string {
	return "google_gmail_send"
}

func (t *GoogleGmailSendTool) Description() string {
	return "Send an email via the user's Gmail account."
}

func (t *GoogleGmailSendTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"to": {"type": "string", "description": "Recipient email address"},
			"subject": {"type": "string", "description": "Email subject"},
			"body": {"type": "string", "description": "Email body (plain text)"}
		},
		"required": ["to", "subject", "body"]
	}`
}

func (t *GoogleGmailSendTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Gmail client: %w", err)
	}

	rawMessage := fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s", args.To, args.Subject, args.Body)
	encoded := base64.URLEncoding.EncodeToString([]byte(rawMessage))

	var msg gmail.Message
	msg.Raw = encoded

	_, err = srv.Users.Messages.Send("me", &msg).Do()
	if err != nil {
		return "", fmt.Errorf("unable to send message: %w", err)
	}

	return ToolResult{
		Summary: "✅ **Email sent successfully!**",
		Artifacts: map[string]interface{}{
			"to":      args.To,
			"subject": args.Subject,
			"status":  "sent",
		},
	}.String(), nil
}

func (t *GoogleGmailSendTool) RequiresConfirmation() bool {
	return true
}

type GoogleGmailSearchTool struct {
	Cfg *config.Config
}

func (t *GoogleGmailSearchTool) Name() string {
	return "google_gmail_search"
}

func (t *GoogleGmailSearchTool) Description() string {
	return "Search for emails in the user's Gmail account using a query. Returns JSON list."
}

func (t *GoogleGmailSearchTool) Parameters() string {
	return `{"type": "object", "properties": {"query": {"type": "string", "description": "Search query (e.g. 'from:boss', 'meeting', 'label:unread')"}, "maxResults": {"type": "integer", "description": "Maximum number of emails to list"}}, "required": ["query"]}`
}

func (t *GoogleGmailSearchTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int64  `json:"maxResults"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 5
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Gmail client: %w", err)
	}

	res, err := srv.Users.Messages.List("me").Q(args.Query).MaxResults(args.MaxResults).Do()
	if err != nil {
		return "", fmt.Errorf("unable to search messages: %w", err)
	}

	if len(res.Messages) == 0 {
		return fmt.Sprintf("No messages found matching query: %s", args.Query), nil
	}

	var messages []GmailMessageInfo
	for _, m := range res.Messages {
		msg, err := srv.Users.Messages.Get("me", m.Id).Format("metadata").MetadataHeaders("Subject", "From", "Date").Do()
		if err != nil {
			continue
		}

		info := GmailMessageInfo{ID: m.Id}
		for _, h := range msg.Payload.Headers {
			if h.Name == "Subject" {
				info.Subject = h.Value
			}
			if h.Name == "From" {
				info.From = h.Value
			}
			if h.Name == "Date" {
				info.Date = h.Value
			}
		}
		messages = append(messages, info)
	}

	if len(messages) > 1 {
		var choices []AmbiguousChoice
		for _, m := range messages {
			label := fmt.Sprintf("%s — %s (%s)", cleanSenderName(m.From), m.Subject, formatEmailDate(m.Date))
			choices = append(choices, AmbiguousChoice{ID: m.ID, Label: label})
		}
		return AmbiguousResult(fmt.Sprintf("I found %d emails matching '%s'. Which one are you interested in?", len(messages), args.Query), choices).String(), nil
	}

	return ToolResult{
		Summary: fmt.Sprintf("Found %d search result(s) for query: %s", len(messages), args.Query),
		Artifacts: map[string]interface{}{
			"type":             "gmail_search",
			"data":             messages,
			"gmail_message_id": messages[0].ID,
		},
	}.String(), nil
}

func (t *GoogleGmailSearchTool) RequiresConfirmation() bool {
	return false
}

type GoogleGmailGetEmailTool struct {
	Cfg *config.Config
}

func (t *GoogleGmailGetEmailTool) Name() string {
	return "google_gmail_get_email"
}

func (t *GoogleGmailGetEmailTool) Description() string {
	return "Retrieve the full content of a specific email. Give either messageId (from a prior search) or query — a Gmail search string like 'from:alice', 'latest', or 'subject:invoice' — and the newest match is opened."
}

func (t *GoogleGmailGetEmailTool) Parameters() string {
	return `{"type": "object", "properties": {"messageId": {"type": "string", "description": "The unique ID of the Gmail message, if known from a previous search"}, "query": {"type": "string", "description": "Gmail search query to locate the email when no ID is known, e.g. 'from:alice unread' or 'subject:invoice'"}}}`
}

func (t *GoogleGmailGetEmailTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		MessageId string `json:"messageId"`
		Query     string `json:"query"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Gmail client: %w", err)
	}

	// A Gmail message ID is an opaque hex string. Nothing the user says out loud
	// contains one, and the planner has no way to invent it — so requiring it
	// made "read my latest email from Alice" impossible to satisfy in a single
	// step. Resolve a natural-language query to an ID here instead.
	msgID := strings.TrimSpace(args.MessageId)
	if msgID == "" {
		q := strings.TrimSpace(args.Query)
		if q == "" || strings.EqualFold(q, "latest") || strings.EqualFold(q, "last") {
			q = "in:inbox"
		}
		list, err := srv.Users.Messages.List("me").Q(q).MaxResults(1).Do()
		if err != nil {
			return "", fmt.Errorf("unable to search for the email: %w", err)
		}
		if len(list.Messages) == 0 {
			return fmt.Sprintf("No email found matching: %s", q), nil
		}
		msgID = list.Messages[0].Id
	}

	msg, err := srv.Users.Messages.Get("me", msgID).Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve message: %w", err)
	}

	// Extract headers
	subject := ""
	from := ""
	date := ""
	for _, h := range msg.Payload.Headers {
		if h.Name == "Subject" {
			subject = h.Value
		}
		if h.Name == "From" {
			from = h.Value
		}
		if h.Name == "Date" {
			date = h.Value
		}
	}

	// Extract body (very basic multi-part parsing)
	body := ""
	if msg.Payload.Body.Data != "" {
		decoded, _ := base64.URLEncoding.DecodeString(msg.Payload.Body.Data)
		body = string(decoded)
	} else if len(msg.Payload.Parts) > 0 {
		// Look for text/plain part
		for _, part := range msg.Payload.Parts {
			if part.MimeType == "text/plain" && part.Body.Data != "" {
				decoded, _ := base64.URLEncoding.DecodeString(part.Body.Data)
				body = string(decoded)
				break
			}
		}
		// If no text/plain, try the first part anyway
		if body == "" && msg.Payload.Parts[0].Body.Data != "" {
			decoded, _ := base64.URLEncoding.DecodeString(msg.Payload.Parts[0].Body.Data)
			body = string(decoded)
		}
	}

	return ToolResult{
		Summary: fmt.Sprintf("Email Subject: %s\nFrom: %s\nDate: %s", subject, from, date),
		Artifacts: map[string]interface{}{
			"type": "gmail_detail",
			"data": map[string]string{
				"id":      args.MessageId,
				"subject": subject,
				"from":    from,
				"date":    date,
				"body":    body,
			},
		},
	}.String(), nil
}

func (t *GoogleGmailGetEmailTool) RequiresConfirmation() bool {
	return false
}
