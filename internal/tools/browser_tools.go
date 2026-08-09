package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// browserOpTimeout bounds a single CDP operation. Chrome can stall
// indefinitely: a page that never fires load, a modal dialog nobody dismisses,
// or a browser the user closed out from under us. The executor runs tasks
// sequentially, so one stalled Run() used to freeze the entire plan — and with
// it the engine's busy flag — with no way back short of killing the process.
const browserOpTimeout = 45 * time.Second

var (
	browserMu          sync.Mutex
	browserAllocCtx    context.Context
	browserAllocCancel context.CancelFunc
	browserTaskCtx     context.Context
	browserTaskCancel  context.CancelFunc
)

// getBrowserContext returns the long-lived chromedp context, creating the
// browser on first use. It is deliberately NOT parented to any caller's ctx:
// the browser outlives individual tool calls. Per-call bounds come from
// runBrowser instead.
func getBrowserContext() context.Context {
	browserMu.Lock()
	defer browserMu.Unlock()
	if browserAllocCtx == nil {
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", false),
		)
		browserAllocCtx, browserAllocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
		browserTaskCtx, browserTaskCancel = chromedp.NewContext(browserAllocCtx)
	}
	return browserTaskCtx
}

// CloseBrowser tears down the shared Chrome instance. Safe to call more than
// once; a later tool call transparently relaunches.
func CloseBrowser() {
	browserMu.Lock()
	defer browserMu.Unlock()
	if browserTaskCancel != nil {
		browserTaskCancel()
		browserTaskCancel = nil
	}
	if browserAllocCancel != nil {
		browserAllocCancel()
		browserAllocCancel = nil
	}
	browserAllocCtx, browserTaskCtx = nil, nil
}

// runBrowser executes actions against the shared browser under two deadlines:
// a hard per-operation timeout, and the caller's own context (so cancelling a
// plan, or shutting the app down, actually stops the browser work instead of
// leaving it to finish on its own).
func runBrowser(ctx context.Context, actions ...chromedp.Action) error {
	runCtx, cancel := context.WithTimeout(getBrowserContext(), browserOpTimeout)
	defer cancel()

	if ctx != nil {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-ctx.Done():
				cancel()
			case <-stop:
			}
		}()
	}

	err := chromedp.Run(runCtx, actions...)
	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("browser did not respond within %s (page stalled, or Chrome was closed): %w", browserOpTimeout, err)
	}
	return err
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
	var text string
	err := runBrowser(ctx,
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

	url := strings.TrimSpace(input.URL)
	if url == "" {
		return "", fmt.Errorf("browser_navigate requires a url")
	}
	// A bare "example.com" is what a planner produces most of the time; without
	// a scheme chromedp treats it as a file path and lands on about:blank.
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}

	if err := runBrowser(ctx, chromedp.Navigate(url)); err != nil {
		return "", fmt.Errorf("failed to navigate: %w", err)
	}

	return fmt.Sprintf("Successfully navigated to %s", url), nil
}

func (t *BrowserNavigateTool) RequiresConfirmation() bool {
	return false
}
