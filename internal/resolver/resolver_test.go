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

type fakeMatcher struct {
	name string
	conf float64
	ok   bool
}

func (f fakeMatcher) Name() string { return f.name }
func (f fakeMatcher) Match(in NormalizedInput) (*Match, bool) {
	if !f.ok {
		return nil, false
	}
	return &Match{Confidence: f.conf, Reason: f.name}, true
}

func TestResolveTakesFirstAboveThreshold(t *testing.T) {
	r := NewResolver(
		fakeMatcher{"low", 0.4, true},
		fakeMatcher{"high", 0.9, true},
	)
	m, ok := r.Resolve(Normalize("x", ""))
	if !ok {
		t.Fatal("expected a match")
	}
	if m.Reason != "high" {
		t.Errorf("expected 'high' matcher to win, got %q", m.Reason)
	}
}

func TestResolveNoMatchWhenAllBelowThreshold(t *testing.T) {
	r := NewResolver(fakeMatcher{"a", 0.5, true}, fakeMatcher{"b", 0.6, true})
	if _, ok := r.Resolve(Normalize("x", "")); ok {
		t.Error("expected no match below threshold 0.7")
	}
}

func TestResolveNoMatchWhenNoneMatch(t *testing.T) {
	r := NewResolver(fakeMatcher{"a", 0.9, false})
	if _, ok := r.Resolve(Normalize("x", "")); ok {
		t.Error("expected no match when matchers decline")
	}
}
