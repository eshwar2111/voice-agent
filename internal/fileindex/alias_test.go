package fileindex

import (
	"slices"
	"testing"
)

func TestDeriveAliases(t *testing.T) {
	a := deriveAliases("Resume_Eshwar_2026.pdf")
	for _, want := range []string{"resume", "eshwar", "cv"} {
		if !slices.Contains(a, want) {
			t.Errorf("aliases %v missing %q", a, want)
		}
	}
	b := deriveAliases("voiceAgentNotes.md")
	if !slices.Contains(b, "voice") || !slices.Contains(b, "agent") {
		t.Errorf("camelCase split failed: %v", b)
	}
}
