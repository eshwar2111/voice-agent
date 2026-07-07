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

func findMicrosoftFile(ctx context.Context, client *http.Client, path string) (*FileReference, error) {
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/root:/%s", path)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("unable to find file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OneDrive API error (%s): %s", resp.Status, string(body))
	}

	var item struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		WebURL string `json:"webUrl"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("unable to parse file info: %w", err)
	}

	return &FileReference{
		ID:     item.ID,
		Name:   item.Name,
		WebURL: item.WebURL,
	}, nil
}

type MicrosoftWordReadTool struct {
	Cfg *config.Config
}

func (t *MicrosoftWordReadTool) Name() string {
	return "microsoft_word_read"
}

func (t *MicrosoftWordReadTool) Description() string {
	return "Read the content of a Word document in OneDrive by file path."
}

func (t *MicrosoftWordReadTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"file_path": {"type": "string", "description": "The path to the Word document in OneDrive (e.g., 'Documents/report.docx')"}
		},
		"required": ["file_path"]
	}`
}

func (t *MicrosoftWordReadTool) RequiresConfirmation() bool {
	return false
}

func (t *MicrosoftWordReadTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetMicrosoftClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	fileRef, err := findMicrosoftFile(ctx, client, args.FilePath)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/items/%s/content", fileRef.ID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("unable to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("unable to download document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OneDrive API error (%s): %s", resp.Status, string(body))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("unable to read document content: %w", err)
	}

	result := fmt.Sprintf("File: %s\nSize: %d bytes\n\n", fileRef.Name, len(content))
	result += "📄 Word document retrieved. Document content is in Office Open XML format (.docx).\n"
	result += fmt.Sprintf("🔗 Open in browser: %s", fileRef.WebURL)

	return result, nil
}

type MicrosoftWordWriteTool struct {
	Cfg *config.Config
}

func (t *MicrosoftWordWriteTool) Name() string {
	return "microsoft_word_write"
}

func (t *MicrosoftWordWriteTool) Description() string {
	return "Create or update a Word document in OneDrive. Creates new document with given content."
}

func (t *MicrosoftWordWriteTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"file_path": {"type": "string", "description": "The path for the new/updated Word document (e.g., 'Documents/notes.docx')"},
			"content": {"type": "string", "description": "The text content to write to the document"},
			"action": {"type": "string", "description": "Either 'create' (new file) or 'append' (add to existing). Default: 'create'"}
		},
		"required": ["file_path", "content"]
	}`
}

func (t *MicrosoftWordWriteTool) RequiresConfirmation() bool {
	return true
}

func (t *MicrosoftWordWriteTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
		Action   string `json:"action"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	if args.Action == "" {
		args.Action = "create"
	}

	client, err := auth.GetMicrosoftClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	if args.Action == "append" {
		existing, err := findMicrosoftFile(ctx, client, args.FilePath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("📝 Document ready to update: %s\n\nNote: Microsoft Graph API doesn't support direct text append to .docx files. Consider creating a new file or using the online editor.\n\n🔗 Open existing document: %s", existing.Name, existing.WebURL), nil
	}

	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/root:/%s:/content", args.FilePath)
	payload := strings.NewReader(args.Content)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, payload)
	if err != nil {
		return "", fmt.Errorf("unable to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("unable to upload document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OneDrive API error (%s): %s", resp.Status, string(body))
	}

	var createdFile struct {
		WebURL string `json:"webUrl"`
		Name   string `json:"name"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&createdFile); err != nil {
		return "", fmt.Errorf("unable to parse upload response: %w", err)
	}

	return fmt.Sprintf("✅ Document created successfully:\nName: %s\n🔗 Open: %s", createdFile.Name, createdFile.WebURL), nil
}
