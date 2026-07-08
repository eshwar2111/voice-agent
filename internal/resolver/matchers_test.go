package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yourname/voice-agent/internal/search"
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

func TestDefaultResolverFileVsWeb(t *testing.T) {
	// Seed a real file index so FileMatcher (backed by search.SearchFiles) has
	// something to find for "resume.ai".
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "resume.ai"), []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to seed test file: %v", err)
	}
	search.InitIndexer(dir)
	deadline := time.Now().Add(5 * time.Second)
	for !search.IsReady && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !search.IsReady {
		t.Fatal("file indexer did not become ready in time")
	}

	r := Default()
	// TLD-like filename must go to FileMatcher (open_file), not WebMatcher (open_website).
	m, ok := r.Resolve(Normalize("open file resume.ai", ""))
	if !ok {
		t.Fatalf("'open file resume.ai' should resolve")
	}
	if m.Tasks[0].Tool == "open_website" {
		t.Errorf("'open file resume.ai' wrongly resolved to open_website, params=%s", m.Tasks[0].Params)
	}
	if m.Tasks[0].Tool != "open_file" {
		t.Errorf("'open file resume.ai' should resolve to open_file, got %s", m.Tasks[0].Tool)
	}
	if !strings.Contains(string(m.Tasks[0].Params), "resume.ai") {
		t.Errorf("params should contain resume.ai, got %s", m.Tasks[0].Params)
	}

	// Guard must not over-fire: a real domain with no file cue still resolves to open_website.
	m2, ok := r.Resolve(Normalize("open notion.io", ""))
	if !ok || m2.Tasks[0].Tool != "open_website" {
		t.Errorf("'open notion.io' should still resolve to open_website, ok=%v", ok)
	}
}

func TestDefaultResolverPriority(t *testing.T) {
	r := Default()
	// "pause" must resolve to media, not fall through
	if m, ok := r.Resolve(Normalize("pause", "")); !ok || m.Tasks[0].Tool != "media_control" {
		t.Errorf("'pause' should resolve to media_control")
	}
	// a domain resolves to open_website
	if m, ok := r.Resolve(Normalize("open github.com", "")); !ok || m.Tasks[0].Tool != "open_website" {
		t.Errorf("'open github.com' should resolve to open_website")
	}
	// gibberish falls through (no local match)
	if _, ok := r.Resolve(Normalize("ponder the meaning of life", "")); ok {
		t.Errorf("open-ended request must fall through to Tier 1")
	}
}
