package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/slides/v1"
)

type GoogleSlidesReadTool struct {
	Cfg *config.Config
}

func (t *GoogleSlidesReadTool) Name() string {
	return "google_slides_read"
}

func (t *GoogleSlidesReadTool) Description() string {
	return "Read the content and structure of a Google Slides presentation by presentation ID."
}

func (t *GoogleSlidesReadTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"presentation_id": {"type": "string", "description": "The Google Slides presentation ID (required). Found in the URL: docs.google.com/presentation/d/PRESENTATION_ID/edit"},
			"name": {"type": "string", "description": "Search for a presentation by name. Used if presentation_id is not provided."}
		},
		"required": []
	}`
}

func (t *GoogleSlidesReadTool) RequiresConfirmation() bool {
	return false
}

func (t *GoogleSlidesReadTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		PresentationID string `json:"presentation_id"`
		Name           string `json:"name"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	// If no presentation ID provided, search by name
	if args.PresentationID == "" {
		if args.Name == "" {
			return "", fmt.Errorf("either presentation_id or name is required")
		}

		driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(client))
		if err != nil {
			return "", fmt.Errorf("unable to retrieve Drive client: %w", err)
		}

		q := fmt.Sprintf("mimeType='application/vnd.google-apps.presentation' and name contains '%s'", sanitizeQuery(args.Name))
		files, err := driveSvc.Files.List().Q(q).PageSize(1).OrderBy("modifiedTime desc").Do()
		if err != nil {
			return "", fmt.Errorf("unable to search for presentation: %w", err)
		}

		if len(files.Files) == 0 {
			return fmt.Sprintf("No Google Slide presentation found matching '%s'", args.Name), nil
		}

		args.PresentationID = files.Files[0].Id
	}

	slidesSvc, err := slides.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Slides client: %w", err)
	}

	presentation, err := slidesSvc.Presentations.Get(args.PresentationID).Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve presentation: %w", err)
	}

	title := presentation.Title
	var content strings.Builder

	content.WriteString(fmt.Sprintf("Presentation: %s\n", title))
	content.WriteString(fmt.Sprintf("Total slides: %d\n\n", len(presentation.Slides)))

	for i, slide := range presentation.Slides {
		content.WriteString(fmt.Sprintf("--- Slide %d ---\n", i+1))

		if slide.PageElements != nil {
			for _, elem := range slide.PageElements {
				if elem.Shape != nil && elem.Shape.Text != nil {
					for _, textElem := range elem.Shape.Text.TextElements {
						if textElem.TextRun != nil {
							contentTxt := strings.TrimSpace(textElem.TextRun.Content)
							if contentTxt != "" {
								content.WriteString(contentTxt + "\n")
							}
						}
					}
				}
			}
		}

		content.WriteString("\n")
	}

	return strings.TrimSpace(content.String()), nil
}

type GoogleSlidesCreateTool struct {
	Cfg *config.Config
}

func (t *GoogleSlidesCreateTool) Name() string {
	return "google_slides_create"
}

func (t *GoogleSlidesCreateTool) Description() string {
	return "Create a new Google Slides presentation with a title and optional slide content."
}

func (t *GoogleSlidesCreateTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"title": {"type": "string", "description": "The title of the new presentation"},
			"slides": {"type": "array", "items": {"type": "object", "properties": {"title": {"type": "string"}, "content": {"type": "string"}}}, "description": "Array of slide objects with title and content text"}
		},
		"required": ["title"]
	}`
}

func (t *GoogleSlidesCreateTool) RequiresConfirmation() bool {
	return true
}

func (t *GoogleSlidesCreateTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Title  string `json:"title"`
		Slides []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"slides"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	slidesSvc, err := slides.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Slides client: %w", err)
	}

	// Create presentation with title slide
	presentation, err := slidesSvc.Presentations.Create(&slides.Presentation{
		Title: args.Title,
	}).Do()
	if err != nil {
		return "", fmt.Errorf("unable to create presentation: %w", err)
	}

	presID := presentation.PresentationId

	// Add content slides
	for i := range args.Slides {
		reqs := []*slides.Request{
			{
				CreateSlide: &slides.CreateSlideRequest{
					InsertionIndex: 1,
					SlideLayoutReference: &slides.LayoutReference{
						PredefinedLayout: "TITLE_AND_BODY",
					},
				},
			},
		}

		_, err := slidesSvc.Presentations.BatchUpdate(presID, &slides.BatchUpdatePresentationRequest{
			Requests: reqs,
		}).Do()
		if err != nil {
			return "", fmt.Errorf("unable to add slide %d: %w", i+1, err)
		}
	}

	return fmt.Sprintf("✅ Presentation created:\nTitle: %s\nURL: https://docs.google.com/presentation/d/%s/edit", args.Title, presID), nil
}

type GoogleSlidesSearchTool struct {
	Cfg *config.Config
}

func (t *GoogleSlidesSearchTool) Name() string {
	return "google_slides_search"
}

func (t *GoogleSlidesSearchTool) Description() string {
	return "Search for Google Slides presentations by name or keywords."
}

func (t *GoogleSlidesSearchTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Search query to find presentations"},
			"max_results": {"type": "integer", "description": "Maximum number of results to return (default 10)"}
		},
		"required": ["query"]
	}`
}

func (t *GoogleSlidesSearchTool) RequiresConfirmation() bool {
	return false
}

func (t *GoogleSlidesSearchTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
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

	q := fmt.Sprintf("mimeType='application/vnd.google-apps.presentation' and name contains '%s'", sanitizeQuery(args.Query))
	files, err := driveSvc.Files.List().Q(q).PageSize(args.MaxResults).OrderBy("modifiedTime desc").Do()
	if err != nil {
		return "", fmt.Errorf("unable to search presentations: %w", err)
	}

	if len(files.Files) == 0 {
		return fmt.Sprintf("No Google Slide presentations found matching '%s'", args.Query), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d presentation(s):\n\n", len(files.Files)))
	for _, file := range files.Files {
		result.WriteString(fmt.Sprintf("📽️ %s\n", file.Name))
		result.WriteString(fmt.Sprintf("   Last modified: %s\n", file.ModifiedTime))
		result.WriteString(fmt.Sprintf("   URL: https://docs.google.com/presentation/d/%s/edit\n\n", file.Id))
	}

	return strings.TrimSpace(result.String()), nil
}
