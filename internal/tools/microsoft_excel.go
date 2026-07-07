package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

type MicrosoftExcelReadTool struct {
	Cfg *config.Config
}

func (t *MicrosoftExcelReadTool) Name() string {
	return "microsoft_excel_read"
}

func (t *MicrosoftExcelReadTool) Description() string {
	return "Read data from an Excel workbook in OneDrive by file name and worksheet."
}

func (t *MicrosoftExcelReadTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"file_path": {"type": "string", "description": "The path to the Excel file in OneDrive (e.g., 'Documents/budget.xlsx')"},
			"worksheet": {"type": "string", "description": "The worksheet name to read from (default: first worksheet)"},
			"range": {"type": "string", "description": "The A1 notation range to read (e.g., 'A1:D10'). Reads entire sheet if not specified."}
		},
		"required": ["file_path"]
	}`
}

func (t *MicrosoftExcelReadTool) RequiresConfirmation() bool {
	return false
}

type FileReference struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	WebURL string `json:"webUrl"`
}

func (t *MicrosoftExcelReadTool) findFileID(ctx context.Context, client *http.Client, path string) (*FileReference, error) {
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

func (t *MicrosoftExcelReadTool) listWorksheets(ctx context.Context, client *http.Client, fileID string) ([]string, error) {
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/items/%s/workbook/worksheets", fileID)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("unable to list worksheets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list worksheets: %s", resp.Status)
	}

	var result struct {
		Value []struct {
			Name string `json:"name"`
		} `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("unable to parse worksheets: %w", err)
	}

	var names []string
	for _, ws := range result.Value {
		names = append(names, ws.Name)
	}

	return names, nil
}

func (t *MicrosoftExcelReadTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		FilePath  string `json:"file_path"`
		Worksheet string `json:"worksheet"`
		Range     string `json:"range"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetMicrosoftClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// Find the file
	fileRef, err := t.findFileID(ctx, client, args.FilePath)
	if err != nil {
		return "", err
	}

	// Determine worksheet name
	if args.Worksheet == "" {
		names, err := t.listWorksheets(ctx, client, fileRef.ID)
		if err != nil {
			return "", fmt.Errorf("unable to get worksheet names: %w", err)
		}
		if len(names) == 0 {
			return fmt.Sprintf("Workbook '%s' has no worksheets.", fileRef.Name), nil
		}
		args.Worksheet = names[0]
	}

	// Build range URL
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/items/%s/workbook/worksheets/%s/usedRange", fileRef.ID, args.Worksheet)
	if args.Range != "" {
		url = fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/items/%s/workbook/worksheets/%s/range(address='%s')", fileRef.ID, args.Worksheet, args.Range)
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("unable to read Excel data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Excel API error (%s): %s", resp.Status, string(body))
	}

	var data struct {
		Address string   `json:"address"`
		Values  [][]any `json:"values"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("unable to parse Excel data: %w", err)
	}

	if len(data.Values) == 0 {
		return fmt.Sprintf("No data found in worksheet '%s' of '%s'", args.Worksheet, fileRef.Name), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s\n", fileRef.Name))
	sb.WriteString(fmt.Sprintf("Worksheet: %s\n", args.Worksheet))
	sb.WriteString(fmt.Sprintf("Range: %s\n\n", data.Address))

	// Format as table
	maxCols := 0
	for _, row := range data.Values {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	for i, row := range data.Values {
		for len(row) < maxCols {
			row = append(row, "")
		}
		if i == 0 {
			sb.WriteString("| ")
			for _, cell := range row {
				sb.WriteString(fmt.Sprintf("%-20s | ", fmt.Sprintf("**%v**", cell)))
			}
			sb.WriteString("\n")
			sb.WriteString("| ")
			for j := 0; j < maxCols; j++ {
				sb.WriteString("-----------------------| ")
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString("| ")
			for _, cell := range row {
				sb.WriteString(fmt.Sprintf("%-20s | ", cell))
			}
			sb.WriteString("\n")
		}
	}

	if fileRef.WebURL != "" {
		sb.WriteString(fmt.Sprintf("\n🔗 Open in browser: %s", fileRef.WebURL))
	}

	return strings.TrimSpace(sb.String()), nil
}

type MicrosoftExcelWriteTool struct {
	Cfg *config.Config
}

func (t *MicrosoftExcelWriteTool) Name() string {
	return "microsoft_excel_write"
}

func (t *MicrosoftExcelWriteTool) Description() string {
	return "Write or update data in an Excel workbook in OneDrive. Can append rows or update specific cells."
}

func (t *MicrosoftExcelWriteTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"file_path": {"type": "string", "description": "The path to the Excel file in OneDrive (e.g., 'Documents/budget.xlsx')"},
			"worksheet": {"type": "string", "description": "The worksheet name to write to"},
			"range": {"type": "string", "description": "The A1 notation range to write to (e.g., 'A1:C10')"},
			"values": {"type": "array", "items": {"type": "array", "items": {"type": "string"}}, "description": "2D array of values to write. Each inner array is a row."}
		},
		"required": ["file_path", "values"]
	}`
}

func (t *MicrosoftExcelWriteTool) RequiresConfirmation() bool {
	return true
}

func (t *MicrosoftExcelWriteTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		FilePath  string `json:"file_path"`
		Worksheet string `json:"worksheet"`
		Range     string `json:"range"`
		Values    [][]any `json:"values"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetMicrosoftClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// Find the file using the Read tool's helper
	readTool := &MicrosoftExcelReadTool{Cfg: t.Cfg}
	fileRef, err := readTool.findFileID(ctx, client, args.FilePath)
	if err != nil {
		return "", err
	}

	// Determine worksheet name
	if args.Worksheet == "" {
		names, err := readTool.listWorksheets(ctx, client, fileRef.ID)
		if err != nil {
			return "", fmt.Errorf("unable to get worksheet names: %w", err)
		}
		if len(names) == 0 {
			return fmt.Sprintf("Workbook '%s' has no worksheets.", fileRef.Name), nil
		}
		args.Worksheet = names[0]
	}

	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/items/%s/workbook/worksheets/%s/usedRange", fileRef.ID, args.Worksheet)
	if args.Range != "" {
		url = fmt.Sprintf("https://graph.microsoft.com/v1.0/me/drive/items/%s/workbook/worksheets/%s/range(address='%s')", fileRef.ID, args.Worksheet, args.Range)
	}

	payload := map[string]interface{}{
		"values": args.Values,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("unable to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PATCH", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("unable to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("unable to write Excel data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Excel API error (%s): %s", resp.Status, string(body))
	}

	return fmt.Sprintf("✅ Successfully updated Excel data in '%s'\nWorksheet: %s", fileRef.Name, args.Worksheet), nil
}
