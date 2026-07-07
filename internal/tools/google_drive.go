package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type GoogleDriveListTool struct {
	Cfg *config.Config
}

func (t *GoogleDriveListTool) Name() string {
	return "google_drive_list"
}

func (t *GoogleDriveListTool) Description() string {
	return "List files from the user's Google Drive. Default is 10 files."
}

func (t *GoogleDriveListTool) Parameters() string {
	return `{"type": "object", "properties": {"maxResults": {"type": "integer", "description": "Maximum number of files to list"}, "query": {"type": "string", "description": "Search query for files (e.g. name contains 'plan')"}}, "required": []}`
}

func (t *GoogleDriveListTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		MaxResults int64  `json:"maxResults"`
		Query      string `json:"query"`
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

	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Drive client: %w", err)
	}

	call := srv.Files.List().PageSize(args.MaxResults).Fields("nextPageToken, files(id, name, mimeType)")
	if args.Query != "" {
		call = call.Q(fmt.Sprintf("name contains '%s'", args.Query))
	}

	res, err := call.Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve files: %w", err)
	}

	if len(res.Files) == 0 {
		return ToolResult{
			Summary: "No Google Drive files found matching the query.",
			Artifacts: map[string]interface{}{
				"type": "drive_list",
				"data": []interface{}{},
			},
		}.String(), nil
	}

	if args.Query != "" && len(res.Files) > 1 {
		var choices []AmbiguousChoice
		for _, f := range res.Files {
			choices = append(choices, AmbiguousChoice{ID: f.Id, Label: f.Name})
		}
		return AmbiguousResult(fmt.Sprintf("I found %d files in your Drive matching '%s'. Which one did you mean?", len(res.Files), args.Query), choices).String(), nil
	}

	result := "Google Drive files:\n"
	var parsedFiles []map[string]string
	for _, f := range res.Files {
		result += fmt.Sprintf("- %s (%s) [ID: %s]\n", f.Name, f.MimeType, f.Id)
		parsedFiles = append(parsedFiles, map[string]string{
			"id":       f.Id,
			"name":     f.Name,
			"mimeType": f.MimeType,
		})
	}

	return ToolResult{
		Summary: result,
		Artifacts: map[string]interface{}{
			"type":          "drive_list",
			"data":          parsedFiles,
			"drive_file_id": res.Files[0].Id,
			"name":          res.Files[0].Name,
		},
	}.String(), nil
}

func (t *GoogleDriveListTool) RequiresConfirmation() bool {
	return false
}
