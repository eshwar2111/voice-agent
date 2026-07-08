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
