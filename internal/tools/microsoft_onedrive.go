package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

type MicrosoftOneDriveListTool struct {
	Cfg *config.Config
}

func (t *MicrosoftOneDriveListTool) Name() string {
	return "microsoft_onedrive_list"
}

func (t *MicrosoftOneDriveListTool) Description() string {
	return "List files and folders in the user's OneDrive or a specific folder."
}

func (t *MicrosoftOneDriveListTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "The folder path in OneDrive to list (e.g., 'Documents/Work'). Leave empty for root directory."},
			"max_results": {"type": "integer", "description": "Maximum number of items to return (default 20)"}
		},
		"required": []
	}`
}

func (t *MicrosoftOneDriveListTool) RequiresConfirmation() bool {
	return false
}

func (t *MicrosoftOneDriveListTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Path       string `json:"path"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	if args.MaxResults <= 0 {
		args.MaxResults = 20
	}

	client, err := auth.GetMicrosoftClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	url := "https://graph.microsoft.com/v1.0/me/drive/root/children?$top=" + fmt.Sprintf("%d&$orderby=name", args.MaxResults)
	if args.Path != "" {
		url = fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/root:/%s:/children?$top=%d&$orderby=name", args.Path, args.MaxResults)
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("unable to list OneDrive files: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OneDrive API error (%s): %s", resp.Status, string(body))
	}

	body, _ := io.ReadAll(resp.Body)

	// Parse the response
	var result struct {
		Value []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Size int64  `json:"size"`
			Folder *struct{} `json:"folder"`
			LastModifiedDateTime string `json:"lastModifiedDateTime"`
			WebURL string `json:"webUrl"`
		} `json:"value"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("unable to parse response: %w", err)
	}

	if len(result.Value) == 0 {
		if args.Path == "" {
			return "Your OneDrive is empty.", nil
		}
		return fmt.Sprintf("Folder '%s' is empty or does not exist.", args.Path), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("OneDrive items%s:\n\n", func() string {
		if args.Path != "" { return " in " + args.Path }
		return ""
	}()))

	for _, item := range result.Value {
		icon := "📄"
		if item.Folder != nil {
			icon = "📁"
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", icon, item.Name))
		if item.Size > 0 {
			sb.WriteString(fmt.Sprintf("   Size: %.2f MB\n", float64(item.Size)/(1024*1024)))
		}
		sb.WriteString(fmt.Sprintf("   Modified: %s\n", item.LastModifiedDateTime))
		if item.WebURL != "" {
			sb.WriteString(fmt.Sprintf("   Link: %s\n", item.WebURL))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String()), nil
}
