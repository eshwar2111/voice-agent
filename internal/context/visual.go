package context

import "strings"

// visualCues are phrases that imply the user is asking about on-screen visual content.
var visualCues = []string{
	"on my screen", "on screen", "on the screen", "on my display",
	"what you see", "what do you see", "read the screen", "the screen",
	"this error", "what am i looking at", "look at this", "see this", "screenshot",
}

// NeedsScreenshot reports whether an instruction needs a screen capture for context.
func NeedsScreenshot(instruction string) bool {
	l := strings.ToLower(instruction)
	for _, cue := range visualCues {
		if strings.Contains(l, cue) {
			return true
		}
	}
	return false
}
