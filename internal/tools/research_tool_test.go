package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchPageText(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, `
			<html>
				<head><title>Test Page</title></head>
				<style> body { color: red; } </style>
				<body>
					<h1>Hello World</h1>
					<p>This is a test paragraph.</p>
					<script> console.log("ignore me"); </script>
					<div>More content here.</div>
				</body>
			</html>
		`)
	}))
	defer server.Close()

	text, err := fetchPageText(server.URL)
	if err != nil {
		t.Fatalf("fetchPageText failed: %v", err)
	}

	// Check if tags and scripts/styles are removed
	if strings.Contains(text, "<style>") || strings.Contains(text, "<script>") {
		t.Errorf("HTML tags not removed: %s", text)
	}
	if strings.Contains(text, "body { color: red; }") {
		t.Errorf("Style content not removed: %s", text)
	}
	if strings.Contains(text, "console.log") {
		t.Errorf("Script content not removed: %s", text)
	}

	// Check if actual text is present
	expectedParts := []string{"Hello World", "This is a test paragraph.", "More content here."}
	for _, part := range expectedParts {
		if !strings.Contains(text, part) {
			t.Errorf("Expected content missing: %s", part)
		}
	}

	fmt.Printf("Cleaned text: %s\n", text)
}
