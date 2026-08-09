package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// resolveSpreadsheetID turns a human-spoken sheet name into a Drive file ID.
//
// A spreadsheet ID is a 44-character opaque string; a user saying "add a row to
// my budget sheet" can never supply one, and neither can the planner. Any tool
// that hard-requires the ID is unreachable by voice, so every sheets tool needs
// this same fallback — hence one helper rather than a copy per tool.
//
// Returns (id, "", nil) on success, or ("", userMessage, nil) when there was
// nothing to match — a missing sheet is an answer for the user, not a failure
// worth aborting the surrounding plan over.
func resolveSpreadsheetID(ctx context.Context, client *http.Client, name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("either spreadsheet_id or name is required")
	}

	driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", "", fmt.Errorf("unable to retrieve Drive client: %w", err)
	}

	q := fmt.Sprintf("mimeType='application/vnd.google-apps.spreadsheet' and name contains '%s'", sanitizeQuery(name))
	files, err := driveSvc.Files.List().Q(q).PageSize(1).OrderBy("modifiedTime desc").Do()
	if err != nil {
		return "", "", fmt.Errorf("unable to search for spreadsheet: %w", err)
	}
	if len(files.Files) == 0 {
		return "", fmt.Sprintf("No Google Sheet found matching '%s'", name), nil
	}
	return files.Files[0].Id, "", nil
}

type GoogleSheetsReadTool struct {
	Cfg *config.Config
}

func (t *GoogleSheetsReadTool) Name() string {
	return "google_sheets_read"
}

func (t *GoogleSheetsReadTool) Description() string {
	return "Read data from a Google Sheet by spreadsheet ID and optional range."
}

func (t *GoogleSheetsReadTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"spreadsheet_id": {"type": "string", "description": "The Google Sheets spreadsheet ID (required). Found in the URL: docs.google.com/spreadsheets/d/SPREADSHEET_ID/edit"},
			"range": {"type": "string", "description": "The A1 notation range to read (e.g., 'Sheet1!A1:D10'). Reads entire sheet if not specified."},
			"name": {"type": "string", "description": "Search for a sheet by name. Used if spreadsheet_id is not provided."}
		},
		"required": []
	}`
}

func (t *GoogleSheetsReadTool) RequiresConfirmation() bool {
	return false
}

func (t *GoogleSheetsReadTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		SpreadsheetID string `json:"spreadsheet_id"`
		Range         string `json:"range"`
		Name          string `json:"name"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// If no spreadsheet ID provided, search by name
	if args.SpreadsheetID == "" {
		id, msg, err := resolveSpreadsheetID(ctx, client, args.Name)
		if err != nil {
			return "", err
		}
		if msg != "" {
			return msg, nil
		}
		args.SpreadsheetID = id
	}

	sheetsSvc, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Sheets client: %w", err)
	}

	// Get spreadsheet metadata to find first sheet name if range not provided
	spreadsheet, err := sheetsSvc.Spreadsheets.Get(args.SpreadsheetID).Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve spreadsheet: %w", err)
	}

	title := spreadsheet.Properties.Title
	readRange := args.Range
	if readRange == "" {
		// Read entire first sheet
		sheetName := "Sheet1"
		if len(spreadsheet.Sheets) > 0 && spreadsheet.Sheets[0].Properties.Title != "" {
			sheetName = spreadsheet.Sheets[0].Properties.Title
		}
		readRange = sheetName
	}

	resp, err := sheetsSvc.Spreadsheets.Values.Get(args.SpreadsheetID, readRange).Do()
	if err != nil {
		return "", fmt.Errorf("unable to read sheet data: %w", err)
	}

	if len(resp.Values) == 0 {
		return fmt.Sprintf("Spreadsheet: %s\nNo data found in range: %s", title, readRange), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Spreadsheet: %s\n", title))
	result.WriteString(fmt.Sprintf("Range: %s\n\n", readRange))

	// Find max columns for formatting
	maxCols := 0
	for _, row := range resp.Values {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	// Format as table
	for i, row := range resp.Values {
		// Pad row to max columns
		for len(row) < maxCols {
			row = append(row, "")
		}

		if i == 0 {
			// Header row formatting
			result.WriteString("| ")
			for _, cell := range row {
				result.WriteString(fmt.Sprintf("%-20s | ", fmt.Sprintf("**%s**", cell)))
			}
			result.WriteString("\n")
			result.WriteString("| ")
			for j := 0; j < maxCols; j++ {
				result.WriteString("-----------------------| ")
			}
			result.WriteString("\n")
		} else {
			result.WriteString("| ")
			for _, cell := range row {
				result.WriteString(fmt.Sprintf("%-20s | ", cell))
			}
			result.WriteString("\n")
		}
	}

	return ToolResult{
		Summary: strings.TrimSpace(result.String()),
		Artifacts: map[string]interface{}{
			"spreadsheet_id": args.SpreadsheetID,
			"title":          title,
			"range":          readRange,
		},
	}.String(), nil
}

type GoogleSheetsWriteTool struct {
	Cfg *config.Config
}

func (t *GoogleSheetsWriteTool) Name() string {
	return "google_sheets_write"
}

func (t *GoogleSheetsWriteTool) Description() string {
	return "Write or update data in a Google Sheet. Can append rows or update specific cells."
}

func (t *GoogleSheetsWriteTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"spreadsheet_id": {"type": "string", "description": "The Google Sheets spreadsheet ID, if known"},
			"name": {"type": "string", "description": "The spreadsheet's name, used to find it when no ID is known (e.g. 'Budget 2026'). One of spreadsheet_id or name is required."},
			"range": {"type": "string", "description": "The A1 notation range (e.g., 'Sheet1!A1' or 'Sheet1!A1:C10')"},
			"values": {"type": "array", "items": {"type": "array", "items": {"type": "string"}}, "description": "2D array of values to write. Each inner array is a row."},
			"append": {"type": "boolean", "description": "If true, append values to end of sheet instead of overwriting. Default: false"}
		},
		"required": ["values"]
	}`
}

func (t *GoogleSheetsWriteTool) RequiresConfirmation() bool {
	return true
}

func (t *GoogleSheetsWriteTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		SpreadsheetID string  `json:"spreadsheet_id"`
		Name          string  `json:"name"`
		Range         string  `json:"range"`
		Values        [][]any `json:"values"`
		Append        bool    `json:"append"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// Same name-lookup fallback google_sheets_read already had. Without it this
	// tool could only ever be driven by an ID the planner had no way to obtain.
	if strings.TrimSpace(args.SpreadsheetID) == "" {
		id, msg, err := resolveSpreadsheetID(ctx, client, args.Name)
		if err != nil {
			return "", err
		}
		if msg != "" {
			return msg, nil
		}
		args.SpreadsheetID = id
	}

	sheetsSvc, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Sheets client: %w", err)
	}

	// Get spreadsheet info for display
	spreadsheet, err := sheetsSvc.Spreadsheets.Get(args.SpreadsheetID).Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve spreadsheet: %w", err)
	}

	title := spreadsheet.Properties.Title

	// Convert [][]any to [][]any (sheets.ValueRange expects this)
	var valueRange sheets.ValueRange
	if args.Range != "" {
		valueRange.Range = args.Range
	}

	// Convert interface{} values to strings for Sheet API
	var values [][]any
	for _, row := range args.Values {
		var strRow []any
		for _, cell := range row {
			strRow = append(strRow, cell)
		}
		values = append(values, strRow)
	}
	valueRange.Values = values

	if args.Append {
		// Append mode: add rows to end of sheet
		sheetName := "Sheet1"
		if len(spreadsheet.Sheets) > 0 && spreadsheet.Sheets[0].Properties.Title != "" {
			sheetName = spreadsheet.Sheets[0].Properties.Title
		}

		resp, err := sheetsSvc.Spreadsheets.Values.Append(args.SpreadsheetID, sheetName, &valueRange).ValueInputOption("USER_ENTERED").InsertDataOption("INSERT_ROWS").Do()
		if err != nil {
			return "", fmt.Errorf("unable to append data: %w", err)
		}

		return ToolResult{
			Summary: fmt.Sprintf("✅ Successfully added %d row(s) to '%s'\nUpdated range: %s", resp.Updates.UpdatedRows, title, resp.Updates.UpdatedRange),
			Artifacts: map[string]interface{}{
				"spreadsheet_id": args.SpreadsheetID,
				"title":          title,
				"updated_range":  resp.Updates.UpdatedRange,
				"updated_rows":   resp.Updates.UpdatedRows,
			},
		}.String(), nil
	}

	// Overwrite mode
	if args.Range == "" {
		return "", fmt.Errorf("range is required for overwrite mode")
	}

	resp, err := sheetsSvc.Spreadsheets.Values.Update(args.SpreadsheetID, args.Range, &valueRange).ValueInputOption("USER_ENTERED").Do()
	if err != nil {
		return "", fmt.Errorf("unable to write data: %w", err)
	}

	return ToolResult{
		Summary: fmt.Sprintf("✅ Successfully updated %d cell(s) in '%s'\nRange: %s", resp.UpdatedCells, title, args.Range),
		Artifacts: map[string]interface{}{
			"spreadsheet_id": args.SpreadsheetID,
			"title":          title,
			"updated_range":  args.Range,
			"updated_cells":  resp.UpdatedCells,
		},
	}.String(), nil
}

type GoogleSheetsSearchTool struct {
	Cfg *config.Config
}

func (t *GoogleSheetsSearchTool) Name() string {
	return "google_sheets_search"
}

func (t *GoogleSheetsSearchTool) Description() string {
	return "Search for Google Sheets spreadsheets by name or keywords."
}

func (t *GoogleSheetsSearchTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query to find spreadsheets"},
			"max_results": {"type": "integer", "description": "Maximum number of results to return (default 10)"}
		},
		"required": ["query"]
	}`
}

func (t *GoogleSheetsSearchTool) RequiresConfirmation() bool {
	return false
}

func (t *GoogleSheetsSearchTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int64  `json:"max_results"`
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

	driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Drive client: %w", err)
	}

	q := fmt.Sprintf("mimeType='application/vnd.google-apps.spreadsheet' and name contains '%s'", sanitizeQuery(args.Query))
	files, err := driveSvc.Files.List().Q(q).PageSize(args.MaxResults).OrderBy("modifiedTime desc").Do()
	if err != nil {
		return "", fmt.Errorf("unable to search spreadsheets: %w", err)
	}

	if len(files.Files) == 0 {
		return fmt.Sprintf("No Google Sheets found matching '%s'", args.Query), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d spreadsheet(s):\n\n", len(files.Files)))
	var choices []AmbiguousChoice
	for _, file := range files.Files {
		result.WriteString(fmt.Sprintf("📊 %s\n", file.Name))
		result.WriteString(fmt.Sprintf("   Last modified: %s\n", file.ModifiedTime))
		result.WriteString(fmt.Sprintf("   URL: https://docs.google.com/spreadsheets/d/%s/edit\n\n", file.Id))
		choices = append(choices, AmbiguousChoice{ID: file.Id, Label: file.Name})
	}

	if len(files.Files) > 1 {
		return AmbiguousResult(fmt.Sprintf("I found %d spreadsheets matching '%s'. Which one should I use?", len(files.Files), args.Query), choices).String(), nil
	}

	return ToolResult{
		Summary: strings.TrimSpace(result.String()),
		Artifacts: map[string]interface{}{
			"search_query":   args.Query,
			"spreadsheet_id": files.Files[0].Id,
			"title":          files.Files[0].Name,
			"type":           "sheets_search",
		},
	}.String(), nil
}
