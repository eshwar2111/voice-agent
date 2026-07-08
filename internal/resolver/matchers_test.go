package resolver

import (
	"strings"
	"testing"
)

func TestDateTimeMatcher(t *testing.T) {
	m := DateTimeMatcher{}
	for _, in := range []string{"what time is it", "what's the date", "current time"} {
		match, ok := m.Match(Normalize(in, ""))
		if !ok || match.Confidence < DefaultThreshold {
			t.Errorf("%q should match datetime, got ok=%v", in, ok)
			continue
		}
		if len(match.Tasks) != 1 || match.Tasks[0].Tool != "get_datetime" {
			t.Errorf("%q should produce get_datetime task", in)
		}
	}
	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("datetime must not match app launch")
	}
}

func TestWebMatcher(t *testing.T) {
	m := WebMatcher{}

	match, ok := m.Match(Normalize("open youtube.com", ""))
	if !ok || len(match.Tasks) != 1 || match.Tasks[0].Tool != "open_website" {
		t.Fatalf("expected open_website for a domain, ok=%v", ok)
	}
	if !strings.Contains(string(match.Tasks[0].Params), "youtube.com") {
		t.Errorf("url param missing domain: %s", match.Tasks[0].Params)
	}

	match, ok = m.Match(Normalize("google golang generics", ""))
	if !ok || match.Tasks[0].Tool != "web_search" {
		t.Fatalf("expected web_search, ok=%v", ok)
	}

	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("web matcher must not claim a bare app name")
	}
}

func TestAppMatcher(t *testing.T) {
	m := AppMatcher{Lookup: func(q string) (string, int) {
		if q == "notepad" {
			return "Notepad", 1
		}
		if q == "word" {
			return "Word", 3 // ambiguous
		}
		return "", 0
	}}

	match, ok := m.Match(Normalize("open notepad", ""))
	if !ok || match.Tasks[0].Tool != "open_app" {
		t.Fatalf("expected open_app, ok=%v", ok)
	}
	if match.Confidence < DefaultThreshold {
		t.Errorf("single strong match should be >= threshold, got %v", match.Confidence)
	}

	// ambiguous -> confidence must drop below threshold so it falls to Tier 1
	amb, ok := m.Match(Normalize("open word", ""))
	if ok && amb.Confidence >= DefaultThreshold {
		t.Errorf("ambiguous app match should be < threshold, got %v", amb.Confidence)
	}

	// no launch verb -> no match
	if _, ok := m.Match(Normalize("what time is it", "")); ok {
		t.Error("app matcher requires a launch verb")
	}
}

func TestFileMatcher(t *testing.T) {
	m := FileMatcher{Search: func(q string) []string {
		if q == "resume.pdf" {
			return []string{`C:\Users\me\resume.pdf`}
		}
		if q == "report" {
			return []string{`C:\a\report.docx`, `C:\b\report.xlsx`}
		}
		return nil
	}}

	match, ok := m.Match(Normalize("open file resume.pdf", ""))
	if !ok || match.Tasks[0].Tool != "open_file" {
		t.Fatalf("expected open_file, ok=%v", ok)
	}
	if !strings.Contains(string(match.Tasks[0].Params), "resume.pdf") {
		t.Errorf("file_path missing: %s", match.Tasks[0].Params)
	}

	amb, ok := m.Match(Normalize("open file report", ""))
	if ok && amb.Confidence >= DefaultThreshold {
		t.Errorf("multiple file hits should be < threshold, got %v", amb.Confidence)
	}

	if _, ok := m.Match(Normalize("open file nothinghere", "")); ok {
		t.Error("no hits -> no match")
	}
}

func TestMediaMatcher(t *testing.T) {
	m := MediaMatcher{}
	cases := map[string]string{
		"pause":          "pause",
		"pause music":    "pause",
		"play":           "play",
		"next track":     "next",
		"previous song":  "previous",
		"volume up":      "volume_up",
		"volume down":    "volume_down",
		"mute":           "mute",
	}
	for phrase, want := range cases {
		match, ok := m.Match(Normalize(phrase, ""))
		if !ok {
			t.Errorf("%q should match media", phrase)
			continue
		}
		if !strings.Contains(string(match.Tasks[0].Params), want) {
			t.Errorf("%q -> want action %q, params=%s", phrase, want, match.Tasks[0].Params)
		}
	}
	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("media must not match app launch")
	}
}

func TestSystemMatcher(t *testing.T) {
	m := SystemMatcher{}
	cases := map[string]string{
		"lock the pc":     "lock",
		"lock computer":   "lock",
		"go to sleep":     "sleep",
		"brightness up":   "brightness_up",
		"brightness down": "brightness_down",
	}
	for phrase, want := range cases {
		match, ok := m.Match(Normalize(phrase, ""))
		if !ok || !strings.Contains(string(match.Tasks[0].Params), want) {
			t.Errorf("%q -> want %q, ok=%v", phrase, want, ok)
		}
	}
	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("system must not match app launch")
	}
}

func TestWindowMatcher(t *testing.T) {
	m := WindowMatcher{}
	cases := map[string]string{
		"minimize window":  "minimize",
		"maximize window":  "maximize",
		"snap left":        "snap_left",
		"snap right":       "snap_right",
		"close window":     "close",
		"switch window":    "switch",
	}
	for phrase, want := range cases {
		match, ok := m.Match(Normalize(phrase, ""))
		if !ok || !strings.Contains(string(match.Tasks[0].Params), want) {
			t.Errorf("%q -> want %q, ok=%v", phrase, want, ok)
		}
	}
	if _, ok := m.Match(Normalize("open notepad", "")); ok {
		t.Error("window must not match app launch")
	}
}
