package tools

import (
	"os"
	"strings"
	"testing"
)

func TestParseDDGResults(t *testing.T) {
	html, err := os.ReadFile("testdata/ddg_sample.html")
	if err != nil {
		t.Fatal(err)
	}
	res := parseDDGResults(string(html), 6)
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(res), res)
	}
	if !strings.Contains(res[0].Title, "Go Programming Language") {
		t.Errorf("title[0]=%q", res[0].Title)
	}
	// URL must be the decoded uddg target, not the duckduckgo redirect.
	if res[0].URL != "https://go.dev/doc/" {
		t.Errorf("url[0]=%q want https://go.dev/doc/", res[0].URL)
	}
	if !strings.Contains(res[0].Snippet, "Official documentation") {
		t.Errorf("snippet[0]=%q", res[0].Snippet)
	}
}

func TestParseDDGResultsGarbageNoPanic(t *testing.T) {
	if got := parseDDGResults("<html>no results here</html>", 6); len(got) != 0 {
		t.Errorf("garbage should yield 0 results, got %d", len(got))
	}
	if got := parseDDGResults("", 6); got == nil || len(got) != 0 {
		t.Errorf("empty should yield empty (non-nil) slice")
	}
}

func TestParseDDGResultsRespectsMax(t *testing.T) {
	html, _ := os.ReadFile("testdata/ddg_sample.html")
	if got := parseDDGResults(string(html), 1); len(got) != 1 {
		t.Errorf("max=1 should cap results, got %d", len(got))
	}
}
