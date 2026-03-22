package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chromedp/chromedp"
)

var (
	browserAllocCtx    context.Context
	browserAllocCancel context.CancelFunc
	browserTaskCtx     context.Context
)

func getBrowserContext() context.Context {
	if browserAllocCtx == nil {
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", false),
		)
		browserAllocCtx, browserAllocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
		browserTaskCtx, _ = chromedp.NewContext(browserAllocCtx)
	}
	return browserTaskCtx
}

// BrowserReadPageTool connects to an existing Chrome instance (or launches one) and extracts the clean text content of the current page using CDP.
type BrowserReadPageTool struct{}

func (t *BrowserReadPageTool) Name() string {
	return "browser_read_page"
}

func (t *BrowserReadPageTool) Description() string {
	return "Connects to an existing Chrome instance (or launches one) and extracts the clean text content of the current page using CDP."
}

func (t *BrowserReadPageTool) Parameters() string {
	return `{"type":"object","properties":{}}`
}

func (t *BrowserReadPageTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	taskCtx := getBrowserContext()

	var text string
	err := chromedp.Run(taskCtx,
		chromedp.Evaluate(`document.body.innerText || document.documentElement.innerText`, &text),
	)
	if err != nil {
		return "", fmt.Errorf("failed to extract page text: %w", err)
	}

	return text, nil
}

func (t *BrowserReadPageTool) RequiresConfirmation() bool {
	return false
}

// BrowserNavigateTool connects to Chrome and navigates the active tab to a provided url.
type BrowserNavigateTool struct{}

func (t *BrowserNavigateTool) Name() string {
	return "browser_navigate"
}

func (t *BrowserNavigateTool) Description() string {
	return "Connects to Chrome and navigates the active tab to a provided url."
}

func (t *BrowserNavigateTool) Parameters() string {
	return `{"type":"object","properties":{"url":{"type":"string","description":"The URL to navigate to"}},"required":["url"]}`
}

func (t *BrowserNavigateTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	taskCtx := getBrowserContext()

	err := chromedp.Run(taskCtx,
		chromedp.Navigate(input.URL),
	)
	if err != nil {
		return "", fmt.Errorf("failed to navigate: %w", err)
	}

	return fmt.Sprintf("Successfully navigated to %s", input.URL), nil
}

func (t *BrowserNavigateTool) RequiresConfirmation() bool {
	return false
}
