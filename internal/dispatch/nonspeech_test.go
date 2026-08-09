package dispatch

import "testing"

// Whisper emits bracketed markers for non-speech audio. Dispatching those as
// user commands sent "[BLANK_AUDIO]" to the cloud orchestrator, which
// obediently planned "apologise and ask the user to repeat" — a token-burning
// loop triggered by silence.
func TestIsNonSpeech(t *testing.T) {
	nonSpeech := []string{
		"", "   ", "\n",
		"[BLANK_AUDIO]", "[blank_audio]", " [BLANK_AUDIO] ",
		"[SILENCE]", "(silence)", "[ Silence ]",
		"[MUSIC]", "[NOISE]", "...", ".",
	}
	for _, s := range nonSpeech {
		if !isNonSpeech(s) {
			t.Errorf("isNonSpeech(%q) = false, want true", s)
		}
	}

	speech := []string{
		"open notepad", "Open Notepad.", "what time is it",
		"play [something] by the band", // brackets mid-sentence are real speech
		"a",
	}
	for _, s := range speech {
		if isNonSpeech(s) {
			t.Errorf("isNonSpeech(%q) = true, want false — real speech must dispatch", s)
		}
	}
}
