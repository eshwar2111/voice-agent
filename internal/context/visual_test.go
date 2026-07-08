package context

import "testing"

func TestNeedsScreenshot(t *testing.T) {
	visual := []string{
		"what's on my screen", "explain this error", "what am i looking at",
		"summarize what you see", "read the screen", "what is this on my display",
	}
	for _, s := range visual {
		if !NeedsScreenshot(s) {
			t.Errorf("%q should need a screenshot", s)
		}
	}
	textual := []string{
		"summarize this", "reply to this email", "what time is it", "open notepad",
	}
	for _, s := range textual {
		if NeedsScreenshot(s) {
			t.Errorf("%q should NOT need a screenshot", s)
		}
	}
}
