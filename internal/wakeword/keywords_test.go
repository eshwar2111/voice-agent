package wakeword

import "testing"

const sampleKeywords = "" +
	"▁HE ▁Y ▁JAR ▁VIS @hey jarvis\n" +
	"▁COM ▁PU ▁TER @computer\n" +
	"▁HE ▁Y ▁NO ▁VA @hey nova\n"

func TestSelectKeywordByLabel(t *testing.T) {
	line, ok := selectKeyword(sampleKeywords, "computer")
	if !ok || line != "▁COM ▁PU ▁TER @computer" {
		t.Fatalf("got %q ok=%v", line, ok)
	}
	// Case-insensitive + surrounding spaces on the requested label.
	if _, ok := selectKeyword(sampleKeywords, "  Hey Jarvis "); !ok {
		t.Error("label match should be case-insensitive and trimmed")
	}
	// Unknown label -> not found (caller falls back to the first line).
	if _, ok := selectKeyword(sampleKeywords, "banana"); ok {
		t.Error("unknown label must return ok=false")
	}
}
