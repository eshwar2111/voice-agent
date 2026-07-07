package tools

// DORMANT — Microsoft suite planned for SP4 (Trustworthy One-Shot Automation). Not registered yet.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

type MicrosoftOutlookListTool struct {
	Cfg *config.Config
}

func (t *MicrosoftOutlookListTool) Name() string {
	return "outlook_list_emails"
}

func (t *MicrosoftOutlookListTool) Description() string {
	return "List recent emails from the user's Microsoft Outlook account using Graph API."
}

func (t *MicrosoftOutlookListTool) Parameters() string {
	return `{"type": "object", "properties": {"top": {"type": "integer", "description": "Number of emails to fetch (max 50)"}}, "required": []}`
}

func (t *MicrosoftOutlookListTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
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

	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/messages?$top=%d&$select=subject,from,receivedDateTime", args.Top)
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
			Subject          string `json:"subject"`
			ReceivedDateTime string `json:"receivedDateTime"`
			From             struct {
				EmailAddress struct {
					Address string `json:"address"`
					Name    string `json:"name"`
				} `json:"emailAddress"`
			} `json:"from"`
		} `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode Graph API response: %w", err)
	}

	if len(result.Value) == 0 {
		return "No Outlook emails found.", nil
	}

	resStr := "Recent Outlook Emails:\n"
	for _, msg := range result.Value {
		resStr += fmt.Sprintf("- From: %s | Subject: %s (%s)\n", 
			msg.From.EmailAddress.Name, msg.Subject, msg.ReceivedDateTime)
	}

	return resStr, nil
}

func (t *MicrosoftOutlookListTool) RequiresConfirmation() bool {
	return false
}
