package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type GoogleDocsReadTool struct {
	Cfg *config.Config
}

func (t *GoogleDocsReadTool) Name() string {
	return "google_docs_read"
}

func (t *GoogleDocsReadTool) Description() string {
	return "Read the content of a Google Doc by document ID or search by name."
}

func (t *GoogleDocsReadTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"document_id": {"type": "string", "description": "The Google Docs document ID (required). Found in the URL: docs.google.com/document/d/DOCUMENT_ID/edit"},
			"name": {"type": "string", "description": "Search for a document by name. Used if document_id is not provided."}
		},
		"required": []
	}`
}

func (t *GoogleDocsReadTool) RequiresConfirmation() bool {
	return false
}

func (t *GoogleDocsReadTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		DocumentID string `json:"document_id"`
		Name       string `json:"name"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// If no document ID provided, search by name
	if args.DocumentID == "" {
		if args.Name == "" {
			return "", fmt.Errorf("either document_id or name is required")
		}

		driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(client))
		if err != nil {
			return "", fmt.Errorf("unable to retrieve Drive client: %w", err)
		}

		q := fmt.Sprintf("mimeType='application/vnd.google-apps.document' and name contains '%s'", sanitizeQuery(args.Name))
		files, err := driveSvc.Files.List().Q(q).PageSize(1).OrderBy("modifiedTime desc").Do()
		if err != nil {
			return "", fmt.Errorf("unable to search for document: %w", err)
		}

		if len(files.Files) == 0 {
			return fmt.Sprintf("No Google Doc found matching '%s'", args.Name), nil
		}

		args.DocumentID = files.Files[0].Id
	}

	docsSvc, err := docs.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Docs client: %w", err)
	}

	doc, err := docsSvc.Documents.Get(args.DocumentID).Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve document: %w", err)
	}

	title := doc.Title
	var content strings.Builder

	// Extract text from structured content
	for _, element := range doc.Body.Content {
		if element.Paragraph != nil {
			for _, pElement := range element.Paragraph.Elements {
				if pElement.TextRun != nil {
					content.WriteString(pElement.TextRun.Content)
				}
			}
		} else if element.Table != nil {
			for _, row := range element.Table.TableRows {
				for _, cell := range row.TableCells {
					for _, cElement := range cell.Content {
						if cElement.Paragraph != nil {
							for _, pElement := range cElement.Paragraph.Elements {
								if pElement.TextRun != nil {
									content.WriteString(pElement.TextRun.Content)
								}
							}
						}
					}
					content.WriteString(" | ")
				}
				content.WriteString("\n")
			}
		}
	}

	result := fmt.Sprintf("Document: %s\n\n%s", title, strings.TrimSpace(content.String()))
	return ToolResult{
		Summary: result,
		Artifacts: map[string]interface{}{
			"document_id": args.DocumentID,
			"title":       title,
		},
	}.String(), nil
}

type GoogleDocsCreateTool struct {
	Cfg *config.Config
}

func (t *GoogleDocsCreateTool) Name() string {
	return "google_docs_create"
}

func (t *GoogleDocsCreateTool) Description() string {
	return "Create a new Google Doc with the given title and content."
}

func (t *GoogleDocsCreateTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"title": {"type": "string", "description": "The title of the new document"},
			"content": {"type": "string", "description": "The content to add to the document"}
		},
		"required": ["title"]
	}`
}

func (t *GoogleDocsCreateTool) RequiresConfirmation() bool {
	return true
}

func (t *GoogleDocsCreateTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	docsSvc, err := docs.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Docs client: %w", err)
	}

	// Create empty document
	doc, err := docsSvc.Documents.Create(&docs.Document{
		Title: args.Title,
	}).Do()
	if err != nil {
		return "", fmt.Errorf("unable to create document: %w", err)
	}

	docID := doc.DocumentId

	// Add content if provided
	if args.Content != "" {
		_, err = docsSvc.Documents.BatchUpdate(docID, &docs.BatchUpdateDocumentRequest{
			Requests: []*docs.Request{
				{
					InsertText: &docs.InsertTextRequest{
						Text:            args.Content,
						EndOfSegmentLocation: &docs.EndOfSegmentLocation{},
					},
				},
			},
		}).Do()
		if err != nil {
			return "", fmt.Errorf("unable to add content to document: %w", err)
		}
	}

	return ToolResult{
		Summary: fmt.Sprintf("✅ Document created successfully:\nTitle: %s\nURL: https://docs.google.com/document/d/%s/edit", args.Title, docID),
		Artifacts: map[string]interface{}{
			"document_id": docID,
			"title":       args.Title,
			"url":         fmt.Sprintf("https://docs.google.com/document/d/%s/edit", docID),
		},
	}.String(), nil
}

type GoogleDocsSearchTool struct {
	Cfg *config.Config
}

func (t *GoogleDocsSearchTool) Name() string {
	return "google_docs_search"
}

func (t *GoogleDocsSearchTool) Description() string {
	return "Search for Google Docs by name or keywords."
}

func (t *GoogleDocsSearchTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query to find documents"},
			"max_results": {"type": "integer", "description": "Maximum number of results to return (default 10)"}
		},
		"required": ["query"]
	}`
}

func (t *GoogleDocsSearchTool) RequiresConfirmation() bool {
	return false
}

func (t *GoogleDocsSearchTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
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

	q := fmt.Sprintf("mimeType='application/vnd.google-apps.document' and name contains '%s'", sanitizeQuery(args.Query))
	files, err := driveSvc.Files.List().Q(q).PageSize(args.MaxResults).OrderBy("modifiedTime desc").Do()
	if err != nil {
		return "", fmt.Errorf("unable to search documents: %w", err)
	}

	if len(files.Files) == 0 {
		return fmt.Sprintf("No Google Docs found matching '%s'", args.Query), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d document(s):\n\n", len(files.Files)))
	for _, file := range files.Files {
		result.WriteString(fmt.Sprintf("📄 %s\n", file.Name))
		result.WriteString(fmt.Sprintf("   Last modified: %s\n", file.ModifiedTime))
		result.WriteString(fmt.Sprintf("   URL: https://docs.google.com/document/d/%s/edit\n\n", file.Id))
	}

	var choices []AmbiguousChoice
	for _, file := range files.Files {
		choices = append(choices, AmbiguousChoice{ID: file.Id, Label: file.Name})
	}

	if len(files.Files) > 1 {
		return AmbiguousResult(fmt.Sprintf("I found %d documents matching '%s'. Which one should I use?", len(files.Files), args.Query), choices).String(), nil
	}

	return ToolResult{
		Summary: strings.TrimSpace(result.String()),
		Artifacts: map[string]interface{}{
			"search_query": args.Query,
			"document_id":  files.Files[0].Id,
			"title":        files.Files[0].Name,
			"type":         "docs_search",
		},
	}.String(), nil
}

func sanitizeQuery(query string) string {
	// Escape single quotes for Drive API query
	return strings.ReplaceAll(query, "'", "\\'")
}
