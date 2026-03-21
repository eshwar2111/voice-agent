package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yourname/voice-agent/internal/automation"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/ui"
	"github.com/yourname/voice-agent/internal/vision"
)

// FindAndClickTool uses Gemini Vision to semantically locate and click an OS element.
type FindAndClickTool struct {
	Detector *vision.UIDetector
}

func (t *FindAndClickTool) Name() string               { return "find_and_click" }
func (t *FindAndClickTool) RequiresConfirmation() bool { return false }
func (t *FindAndClickTool) Description() string {
	return "Spatially locate a UI element on the screen using a semantic description, then highlight and click it."
}
func (t *FindAndClickTool) Parameters() string {
	return `{"element": {"type": "string", "description": "Semantic description of the UI element (e.g. 'blue Download button', 'Search input field')"}, "action": {"type": "string", "description": "Optional: 'click' (default), 'right', or 'double'"}}`
}

type FindAndClickArgs struct {
	Element string `json:"element"`
	Action  string `json:"action"`
}

func (t *FindAndClickTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params FindAndClickArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	elementDesc := params.Element
	if elementDesc == "" {
		return "", fmt.Errorf("find_and_click: 'element' description is required")
	}

	action := params.Action
	if action == "" {
		action = "left"
	}
	if action == "click" {
		action = "left"
	}

	ui.ShowAutomationStep(fmt.Sprintf("Scanning screen for: %s", elementDesc))

	var element *vision.UIElement
	var lastErr error
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		// 1. Capture Screen
		var screenshot []byte
		var err error
		screenshot, err = automation.CaptureScreen()
		if err != nil {
			return "", fmt.Errorf("find_and_click: failed to capture screen: %w", err)
		}

		// 2. Detect Element via Vision Model
		element, lastErr = t.Detector.FindUIElement(ctx, screenshot, elementDesc)
		if lastErr != nil {
			return "", fmt.Errorf("find_and_click: vision model failed: %w", lastErr)
		}

		if element.Found {
			break
		}

		if i < maxRetries-1 {
			ui.ShowAutomationStep(fmt.Sprintf("Element not found yet, retrying (%d/%d)...", i+1, maxRetries))
			if err := automation.SleepCtx(ctx, 1.0); err != nil {
				return "", err
			}
		}
	}

	if !element.Found {
		ui.ShowAutomationStep(fmt.Sprintf("Element not found: %s", element.Reason))
		return "", fmt.Errorf("visual_search_failed: could not find '%s' on screen. Reason: %s", elementDesc, element.Reason)
	}

	// 3. Highlight the found bounding box natively
	ui.ShowAutomationStep(fmt.Sprintf("Found target at (%d, %d)", element.Center.X, element.Center.Y))
	ui.FlashHighlightBox(element.Box.X, element.Box.Y, element.Box.Width, element.Box.Height, 800) // Show it slightly longer

	// Wait for human to register the highlight
	if err := automation.SleepCtx(ctx, 0.8); err != nil {
		return "", err
	}

	// Explicitly hide the box before clicking so it doesn't eat the click!
	ui.HideHighlightBox()
	if err := automation.SleepCtx(ctx, 0.1); err != nil {
		return "", err
	}

	// 4. Move and Click
	ui.ShowAutomationStep(fmt.Sprintf("Clicking element: %s", elementDesc))
	if action == "double" {
		if err := automation.DoubleClick(element.Center.X, element.Center.Y); err != nil {
			return "", err
		}
	} else {
		if err := automation.ClickMouse(element.Center.X, element.Center.Y, action); err != nil {
			return "", err
		}
	}

	// Wait briefly for UI to respond before next tool (e.g. keyboard_type)
	if err := automation.SleepCtx(ctx, 0.8); err != nil {
		return "", err
	}

	return fmt.Sprintf("Successfully clicked '%s' at absolute coordinates (%d, %d)", elementDesc, element.Center.X, element.Center.Y), nil
}

// ScrollAndFindTool scrolls the screen recursively until an element becomes visible.
type ScrollAndFindTool struct {
	Detector *vision.UIDetector
}

func (t *ScrollAndFindTool) Name() string               { return "scroll_and_find" }
func (t *ScrollAndFindTool) RequiresConfirmation() bool { return false }
func (t *ScrollAndFindTool) Description() string {
	return "Scroll the active window to find an off-screen UI element, then highlight and click it."
}
func (t *ScrollAndFindTool) Parameters() string {
	return `{"element": {"type": "string"}, "direction": {"type": "string", "description": "'down' or 'up'"}, "max_scrolls": {"type": "string", "description": "Maximum number of screen scrolls to attempt (default: 3)"}}`
}

type ScrollAndFindArgs struct {
	Element    string `json:"element"`
	Direction  string `json:"direction"`
	MaxScrolls string `json:"max_scrolls"`
}

func (t *ScrollAndFindTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params ScrollAndFindArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	elementDesc := params.Element
	if elementDesc == "" {
		return "", fmt.Errorf("scroll_and_find: 'element' description is required")
	}

	dir := params.Direction
	if dir == "" {
		dir = "down"
	}

	maxScrolls := 3
	if v, err := strconv.Atoi(params.MaxScrolls); err == nil && v > 0 {
		maxScrolls = v
	}

	for i := 0; i < maxScrolls; i++ {
		ui.ShowAutomationStep(fmt.Sprintf("Scrolling %s (attempt %d/%d) to find: %s", dir, i+1, maxScrolls, elementDesc))

		// Capture and search with retries
		var element *vision.UIElement
		var found bool
		for retry := 0; retry < 3; retry++ {
			screenshot, err := automation.CaptureScreen()
			if err == nil {
				element, err = t.Detector.FindUIElement(ctx, screenshot, elementDesc)
				if err == nil && element.Found {
					found = true
					break
				}
			}
			if retry < 2 {
				if err := automation.SleepCtx(ctx, 1.0); err != nil {
					return "", err
				}
			}
		}

		if found {
			// Found it! Highlight and click.
			ui.ShowAutomationStep(fmt.Sprintf("Found target off-screen! Clicking..."))
			ui.FlashHighlightBox(element.Box.X, element.Box.Y, element.Box.Width, element.Box.Height, 800)

			if err := automation.SleepCtx(ctx, 0.8); err != nil {
				return "", err
			}
			ui.HideHighlightBox()
			if err := automation.SleepCtx(ctx, 0.1); err != nil {
				return "", err
			}

			if err := automation.ClickMouse(element.Center.X, element.Center.Y, "left"); err != nil {
				return "", err
			}
			if err := automation.SleepCtx(ctx, 0.8); err != nil {
				return "", err
			}

			return fmt.Sprintf("Scrolled and found '%s' at (%d, %d)", elementDesc, element.Center.X, element.Center.Y), nil
		}

		// Not found on this screen, perform the scroll
		if dir == "down" {
			if err := automation.ScrollMouse(-15); err != nil {
				return "", err
			} // robotgo usually treats - as down for MacOS/Win, depends on native. Let's explicitly do 15 lines.
		} else {
			if err := automation.ScrollMouse(15); err != nil {
				return "", err
			}
		}

		// Wait for scroll animation and rendering
		if err := automation.SleepCtx(ctx, 1.0); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("scroll_failed: scrolled %d times but could not find '%s'", maxScrolls, elementDesc)
}

// VerifyScreenStateTool captures the screen and asks the LLM to verify a condition.
type VerifyScreenStateTool struct {
	Provider llm.Provider
}

func (t *VerifyScreenStateTool) Name() string               { return "verify_screen_state" }
func (t *VerifyScreenStateTool) RequiresConfirmation() bool { return false }
func (t *VerifyScreenStateTool) Description() string {
	return "Capture the screen and visually verify if a specific UI condition is met (e.g. 'Has the file finished downloading?'). Returns true/false and an explanation."
}
func (t *VerifyScreenStateTool) Parameters() string {
	return `{"condition": {"type": "string", "description": "The visual condition to verify"}}`
}

type VerifyScreenStateArgs struct {
	Condition string `json:"condition"`
}

func (t *VerifyScreenStateTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	var params VerifyScreenStateArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	condition := params.Condition
	if condition == "" {
		return "", fmt.Errorf("verify_screen_state: 'condition' is required")
	}

	ui.ShowAutomationStep(fmt.Sprintf("Verifying visually: %s", condition))

	screenshot, err := automation.CaptureScreen()
	if err != nil {
		return "", fmt.Errorf("failed to capture screen: %w", err)
	}

	prompt := fmt.Sprintf(`Look at this screenshot and verify the following condition: "%s".
Answer YES or NO, followed by a brief explanation of how you know.
Format: YES: <reason> or NO: <reason>`, condition)

	resp, err := t.Provider.Generate(ctx, prompt, [][]byte{screenshot})
	if err != nil {
		return "", fmt.Errorf("llm verification failed: %w", err)
	}

	resp = strings.TrimSpace(resp)
	if strings.HasPrefix(strings.ToUpper(resp), "YES") {
		ui.ShowAutomationStep("✅ Visual verification passed.")
		return "Verification Passed: " + resp, nil
	}

	ui.ShowAutomationStep("❌ Visual verification failed.")
	return "Verification Failed: " + resp, nil
}
