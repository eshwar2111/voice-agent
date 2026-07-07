package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"github.com/yourname/voice-agent/internal/llm"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// GoogleAIAssistantTool — AI-powered Google Workspace agent that can
// compose emails, draft documents, create multi-sheet reports, analyse data,
// and write results directly into Google sheets / docs.
type GoogleAIAssistantTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *GoogleAIAssistantTool) Name() string { return "google_ai_agent" }
func (t *GoogleAIAssistantTool) Description() string {
	return "AI-powered Google Workspace agent. Can compose emails, draft documents, create spreadsheet reports, analyse data, and write results directly into Google services."
}
func (t *GoogleAIAssistantTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"action": {"type": "string", "description": "Action: 'compose_email', 'create_doc', 'create_sheet_report', 'analyse_sheet', 'smart_research_sheet', 'draft_presentation'"},
			"task": {"type": "string", "description": "Natural language task description. e.g., 'Draft an email about sprint review', 'Create a budget report', 'Analyse sales trends in sheet xyz'"},
			"params": {"type": "object", "description": "Additional parameters: recipient, title, sheet_id, etc."}
		},
		"required": ["action", "task"]
	}`
}
func (t *GoogleAIAssistantTool) RequiresConfirmation() bool { return true }

func (t *GoogleAIAssistantTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Action string                 `json:"action"`
		Task   string                 `json:"task"`
		Extra  map[string]interface{} `json:"params"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	switch args.Action {
	case "compose_email":
		return t.composeEmail(ctx, args.Task, args.Extra)
	case "create_doc":
		return t.createDoc(ctx, client, args.Task, args.Extra)
	case "create_sheet_report":
		return t.createSheetReport(ctx, client, args.Task, args.Extra)
	case "analyse_sheet":
		return t.analyseSheet(ctx, client, args.Task, args.Extra)
	case "smart_research_sheet":
		return t.smartResearchSheet(ctx, client, args.Task, args.Extra)
	case "draft_presentation":
		return t.draftPresentation(ctx, args.Task)
	default:
		return fmt.Sprintf("Unknown action: %s. Supported: compose_email, create_doc, create_sheet_report, analyse_sheet, smart_research_sheet, draft_presentation", args.Action), nil
	}
}

func (t *GoogleAIAssistantTool) composeEmail(_ context.Context, task string, extra map[string]interface{}) (string, error) {
	recipient := ""
	if extra != nil {
		if r, ok := extra["recipient"].(string); ok {
			recipient = r
		}
	}

	prompt := fmt.Sprintf(`You are an executive assistant writing a professional email.

TASK: %s
RECIPIENT: %s

Return ONLY a JSON object with this format:
{"subject": "Clear subject line", "body": "Professional email body"}

Rules:
- Write in clear, professional tone
- Include a clear subject line
- Use proper paragraphs
- Keep it to the point`, task, recipient)

	response, err := t.Provider.Generate(context.Background(), prompt, nil)
	if err != nil {
		return "", fmt.Errorf("LLM failed: %w", err)
	}

	jsonStr := extractJSONText(response)
	var email struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &email); err != nil {
		return fmt.Sprintf("Email draft (AI-generated):\n\nSubject: %s\n\n%s", task, response), nil
	}

	result := fmt.Sprintf("📧 Email Draft\n\nTo: %s\nSubject: %s\n\n%s", recipient, email.Subject, email.Body)
	return result, nil
}

func (t *GoogleAIAssistantTool) createDoc(ctx context.Context, client *http.Client, task string, extra map[string]interface{}) (string, error) {
	prompt := fmt.Sprintf(`You are a document author. Create a well-structured document based on this task:

TASK: %s

Return ONLY plain text document content.
Rules:
- Write in plain text (no markdown symbols like **, *, _)
- Use clear section headings
- Include relevant details and context
- Aim for 300-500 words`, task)

	content, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("LLM failed: %w", err)
	}

	// Determine title
	title := task
	if extra != nil {
		if titleVal, ok := extra["title"].(string); ok && titleVal != "" {
			title = titleVal
		}
	}

	// Create the Google Document
	docsSvc, err := docs.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to create Docs client: %w", err)
	}

	doc, err := docsSvc.Documents.Create(&docs.Document{Title: title}).Do()
	if err != nil {
		return "", fmt.Errorf("failed to create doc: %w", err)
	}

	// Add content
	_, err = docsSvc.Documents.BatchUpdate(doc.DocumentId, &docs.BatchUpdateDocumentRequest{
		Requests: []*docs.Request{{
			InsertText: &docs.InsertTextRequest{
				Text:            content,
				EndOfSegmentLocation: &docs.EndOfSegmentLocation{},
			},
		}},
	}).Do()
	if err != nil {
		return "", fmt.Errorf("failed to add content: %w", err)
	}

	preview := content
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}
	return fmt.Sprintf("📄 Document Created: %s\nURL: https://docs.google.com/document/d/%s/edit\n\nPreview:\n%s", title, doc.DocumentId, preview), nil
}

func (t *GoogleAIAssistantTool) createSheetReport(ctx context.Context, client *http.Client, task string, extra map[string]interface{}) (string, error) {
	// 1. LLM generates report structure
	prompt := fmt.Sprintf(`You are a data analyst creating a Google Sheets report.

TASK: %s

Return ONLY a JSON object with this format:
{"title": "Report Title", "headers": ["Col1", "Col2", ...], "data": [["r1c1", "r1c2"], ["r2c1", "r2c2"], ...]}

Rules:
- Include 10-15 rows of realistic, well-structured data
- Headers should be clear and descriptive
- Data should be consistent
- Numbers should be formatted properly`, task)

	response, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("LLM failed: %w", err)
	}

	jsonStr := extractJSONText(response)
	var report struct {
		Title   string     `json:"title"`
		Headers []string   `json:"headers"`
		Data    [][]any    `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &report); err != nil {
		return fmt.Sprintf("Report structure:\n%s", response), nil
	}

	// 2. Create the sheet
	sheetsSvc, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to create Sheets client: %w", err)
	}

	spreadsheet := &sheets.Spreadsheet{
		Properties: &sheets.SpreadsheetProperties{
			Title: report.Title,
		},
	}
	s, err := sheetsSvc.Spreadsheets.Create(spreadsheet).Do()
	if err != nil {
		return "", fmt.Errorf("failed to create sheet: %w", err)
	}

	// 3. Write headers + data
	sheetId := s.SpreadsheetId
	sheetName := s.Sheets[0].Properties.Title
	var values [][]any
	// Convert headers to []any
	for _, h := range report.Headers {
		values = append(values, []any{h})
	}
	// Flatten: each row becomes a row in the sheet
	values = values[:0]
	headerRow := make([]any, len(report.Headers))
	for i, h := range report.Headers {
		headerRow[i] = h
	}
	values = append(values, headerRow)
	for _, row := range report.Data {
		values = append(values, row)
	}

	vr := &sheets.ValueRange{
		Range:  fmt.Sprintf("%s!A1", sheetName),
		Values: values,
	}

	_, err = sheetsSvc.Spreadsheets.Values.Update(sheetId, fmt.Sprintf("%s!A1", sheetName), vr).ValueInputOption("USER_ENTERED").Do()
	if err != nil {
		return "", fmt.Errorf("failed to write sheet data: %w", err)
	}

	result := fmt.Sprintf("📊 Sheet Report Created: %s\nURL: https://docs.google.com/spreadsheets/d/%s/edit\nHeaders: %v\nData rows: %d", report.Title, sheetId, report.Headers, len(report.Data))
	return result, nil
}

func (t *GoogleAIAssistantTool) analyseSheet(ctx context.Context, client *http.Client, task string, extra map[string]interface{}) (string, error) {
	sheetID, ok := extra["sheet_id"].(string)
	if !ok || sheetID == "" {
		return "", fmt.Errorf("sheet_id required in params")
	}

	sheetSvc, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to create Sheets client: %w", err)
	}

	resp, err := sheetSvc.Spreadsheets.Values.Get(sheetID, "").Do()
	if err != nil {
		return "", fmt.Errorf("failed to read sheet: %w", err)
	}

	if len(resp.Values) == 0 {
		return "Sheet is empty.", nil
	}

	// Convert sheet data to text for LLM
	var dataText strings.Builder
	for _, row := range resp.Values {
		var cells []string
		for _, cell := range row {
			if s, ok := cell.(string); ok {
				cells = append(cells, s)
			} else if n, ok := cell.(float64); ok {
				cells = append(cells, fmt.Sprintf("%.2f", n))
			} else {
				cells = append(cells, fmt.Sprintf("%v", cell))
			}
		}
		dataText.WriteString(strings.Join(cells, " | ") + "\n")
	}

	analysisPrompt := fmt.Sprintf(`You are a data analyst. Analyse this spreadsheet data and provide insights:

TASK: %s

SPREADSHEET DATA:
%s

Provide:
1. Key insights and patterns
2. Trends (increasing/decreasing)
3. Anomalies or outliers
4. Summary statistics
5. Actionable recommendations`, task, dataText.String())

	analysis, err := t.Provider.Generate(ctx, analysisPrompt, nil)
	return fmt.Sprintf("📊 Spreadsheet Analysis\n\n%s", analysis), nil
}

func (t *GoogleAIAssistantTool) smartResearchSheet(ctx context.Context, client *http.Client, task string, extra map[string]interface{}) (string, error) {
	sheetID, ok := extra["sheet_id"].(string)
	if !ok || sheetID == "" {
		return "", fmt.Errorf("sheet_id required in params")
	}

	prompt := fmt.Sprintf(`You are a research analyst updating a spreadsheet.

TASK: %s

Return ONLY JSON with this format:
{"new_rows": [["val1", "val2", ...], ["val1", "val2", ...], ...]}

Generate 5-10 new rows of relevant research data that match the existing sheet structure.`, task)

	response, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("LLM research failed: %w", err)
	}

	jsonStr := extractJSONText(response)
	var result struct {
		Rows [][]any `json:"new_rows"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return fmt.Sprintf("AI research output:\n%s", response), nil
	}

	sheetSvc, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to create Sheets client: %w", err)
	}

	vr := &sheets.ValueRange{Values: result.Rows}
	_, err = sheetSvc.Spreadsheets.Values.Append(sheetID, "", vr).ValueInputOption("USER_ENTERED").InsertDataOption("INSERT_ROWS").Do()
	if err != nil {
		return "", fmt.Errorf("failed to write research data: %w", err)
	}

	return fmt.Sprintf("✅ AI research added to sheet!\nAdded %d new rows based on: %s", len(result.Rows), task), nil
}

func (t *GoogleAIAssistantTool) draftPresentation(_ context.Context, task string) (string, error) {
	prompt := fmt.Sprintf(`You are a presentation designer. Create a presentation structure.

TASK: %s

Return ONLY a JSON array of slides:
[{"title": "Slide 1 Title", "content": "Bullet points for slide 1"}, {"title": "Slide 2", "content": "..."}]

Rules:
- 8-12 slides
- Each slide: clear title, concise content
- Include intro and conclusion slides
- Keep text impactful`, task)

	response, err := t.Provider.Generate(context.Background(), prompt, nil)
	if err != nil {
		return "", fmt.Errorf("LLM failed: %w", err)
	}

	jsonStr := extractJSONText(response)
	var slides []struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &slides); err != nil {
		return fmt.Sprintf("Presentation structure:\n%s", response), nil
	}

	result := fmt.Sprintf("🎤 AI Presentation Draft\nTask: %s\n\n", task)
	for i, slide := range slides {
		result += fmt.Sprintf("Slide %d: %s\n%s\n\n", i+1, slide.Title, slide.Content)
	}
	result += "---\nTo create as Google Slides, use google_slides_create with this structure."
	return result, nil
}

func extractJSONText(text string) string {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start != -1 && end > start {
		return text[start : end+1]
	}
	start = strings.Index(text, "{")
	end = strings.LastIndex(text, "}")
	if start != -1 && end > start {
		return text[start : end+1]
	}
	return ""
}
