package resolver

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	in := Normalize("  Open  Notepad  ", "chrome.exe")
	if in.Lower != "open notepad" {
		t.Errorf("Lower = %q, want %q", in.Lower, "open notepad")
	}
	if !reflect.DeepEqual(in.Tokens, []string{"open", "notepad"}) {
		t.Errorf("Tokens = %v, want [open notepad]", in.Tokens)
	}
	if in.Raw != "  Open  Notepad  " {
		t.Errorf("Raw not preserved")
	}
	if in.ActiveApp != "chrome.exe" {
		t.Errorf("ActiveApp = %q", in.ActiveApp)
	}
}
