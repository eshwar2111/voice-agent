package wakeword

import "testing"

// Real sherpa keyword-file format: space-separated tokens then a SINGLE-token
// "@label" (underscores for multi-word phrases). The phone tokens here are the
// real ARPAbet ones the shipped models/kws/keywords.txt uses.
const sampleKeywords = "" +
	"HH EY1 JH AA1 R V AH0 S @hey_jarvis\n" +
	"K AH0 M P Y UW1 T ER0 @computer\n" +
	"HH EY1 N OW1 V AH0 @hey_nova\n"

func TestSelectKeywordByLabel(t *testing.T) {
	line, ok := selectKeyword(sampleKeywords, "computer")
	if !ok || line != "K AH0 M P Y UW1 T ER0 @computer" {
		t.Fatalf("got %q ok=%v", line, ok)
	}
	// The key case: the human-facing config value is SPACED ("hey jarvis") but
	// the file label is a single underscore token ("@hey_jarvis") — they must match.
	line, ok = selectKeyword(sampleKeywords, "  Hey Jarvis ")
	if !ok || line != "HH EY1 JH AA1 R V AH0 S @hey_jarvis" {
		t.Fatalf("spaced query must match underscore label: got %q ok=%v", line, ok)
	}
	// Unknown label -> not found (caller falls back to the first line).
	if _, ok := selectKeyword(sampleKeywords, "banana"); ok {
		t.Error("unknown label must return ok=false")
	}
}

func TestFirstKeywordFallback(t *testing.T) {
	line, ok := firstKeyword(sampleKeywords)
	if !ok || line != "HH EY1 JH AA1 R V AH0 S @hey_jarvis" {
		t.Fatalf("firstKeyword got %q ok=%v", line, ok)
	}
}
