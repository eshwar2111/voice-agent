package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/vision"
)

type ScreenshotAnalysisTool struct {
	Provider llm.Provider
}

func (s *ScreenshotAnalysisTool) Name() string {
	return "analyze_screen"
}

func (s *ScreenshotAnalysisTool) Description() string {
	return "Takes a screenshot of the current screen and returns a descriptive analysis of its contents."
}

func (s *ScreenshotAnalysisTool) Parameters() string {
	return `{}`
}

func (s *ScreenshotAnalysisTool) RequiresConfirmation() bool {
	return false
}

func (s *ScreenshotAnalysisTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	frames, err := vision.CaptureSequence(3)
	if err != nil {
		return "", err
	}

	var images [][]byte
	for _, f := range frames {
		images = append(images, f.Image)
	}

	prompt := "Explain what is visible across these 3 sequential frames of my screen."
	response, err := s.Provider.Generate(ctx, prompt, images)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Screen Analysis:\n%s", response), nil
}
