package resolver

import "testing"

// Whisper transcribes with punctuation: "Open Notepad." not "open notepad".
// Trailing punctuation must not defeat matching, or Tier 0 is bypassed for
// essentially every spoken command.
func TestNormalizeStripsTrailingPunctuation(t *testing.T) {
	for _, raw := range []string{"Open Notepad.", "open notepad!", "Open Notepad?", "open notepad,"} {
		got := Normalize(raw, "").Lower
		if got != "open notepad" {
			t.Errorf("Normalize(%q).Lower = %q, want \"open notepad\"", raw, got)
		}
	}
}
