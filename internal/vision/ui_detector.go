package vision

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yourname/voice-agent/internal/automation"
	"github.com/yourname/voice-agent/internal/llm"
)

type UIDetector struct {
	Provider llm.Provider
}

// UIElement contains the absolute screen coordinates of a located element.
type UIElement struct {
	Found  bool
	Center struct {
		X int
		Y int
	}
	Box struct { // absolute screen pixels
		X      int
		Y      int
		Width  int
		Height int
	}
	Reason string // Explanation from the model if not found
}

// Output schema from Gemini
type geminiBoundingBox struct {
	Found  bool   `json:"found"`
	Box    []int  `json:"box_2d"` // [ymin, xmin, ymax, xmax] normalized 0-1000
	Reason string `json:"reason,omitempty"`
}

// FindUIElement uses the LLM to locate an element spatially inside a screenshot.
func (d *UIDetector) FindUIElement(ctx context.Context, screenshot []byte, description string) (*UIElement, error) {
	prompt := fmt.Sprintf(`Analyze this screenshot and locate the UI element matching this description: "%s".
You must reply ONLY with a valid JSON object matching this structure:
{
  "found": true/false,
  "box_2d": [ymin, xmin, ymax, xmax],
  "reason": "If found=false, explain why."
}

CRITICAL RULES:
- The "box_2d" array MUST contain exactly 4 integers representing the bounding box in a normalized 0-1000 coordinate system.
- ymin is the top edge (0=top, 1000=bottom).
- xmin is the left edge (0=left, 1000=right).
- DO NOT INCLUDE ANY MARKDOWN formatting. Just the raw JSON object.`, description)

	resp, err := d.Provider.Generate(ctx, prompt, [][]byte{screenshot})
	if err != nil {
		return nil, fmt.Errorf("vision API error: %w", err)
	}

	// Clean up markdown just in case
	resp = strings.TrimSpace(resp)
	if strings.HasPrefix(resp, "```json") {
		resp = strings.TrimPrefix(resp, "```json")
	} else if strings.HasPrefix(resp, "```") {
		resp = strings.TrimPrefix(resp, "```")
	}
	if before, ok := strings.CutSuffix(resp, "```"); ok {
		resp = before
	}
	resp = strings.TrimSpace(resp)

	var rawBox geminiBoundingBox
	if err := json.Unmarshal([]byte(resp), &rawBox); err != nil {
		return nil, fmt.Errorf("failed to parse vision JSON (%s): %w", resp, err)
	}

	result := &UIElement{
		Found:  rawBox.Found,
		Reason: rawBox.Reason,
	}

	if rawBox.Found && len(rawBox.Box) == 4 {
		screenW, screenH := automation.GetScreenSize()

		ymin := float64(rawBox.Box[0]) / 1000.0 * float64(screenH)
		xmin := float64(rawBox.Box[1]) / 1000.0 * float64(screenW)
		ymax := float64(rawBox.Box[2]) / 1000.0 * float64(screenH)
		xmax := float64(rawBox.Box[3]) / 1000.0 * float64(screenW)

		result.Box.X = int(xmin)
		result.Box.Y = int(ymin)
		result.Box.Width = int(xmax - xmin)
		result.Box.Height = int(ymax - ymin)

		result.Center.X = result.Box.X + (result.Box.Width / 2)
		result.Center.Y = result.Box.Y + (result.Box.Height / 2)
	}

	return result, nil
}
