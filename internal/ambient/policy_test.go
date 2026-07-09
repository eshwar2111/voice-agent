package ambient

import (
	"testing"
	"time"
)

func TestPolicy(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	p := NewPolicy(2 * time.Minute)
	s := Suggestion{DedupKey: "a"}

	if !p.Allow(s, t0, false) {
		t.Fatal("first suggestion should be allowed")
	}
	if p.Allow(s, t0, true) {
		t.Fatal("must be suppressed while busy")
	}
	p.Record(s, t0)
	if p.Allow(s, t0.Add(30*time.Second), false) {
		t.Fatal("duplicate key within window must be blocked")
	}
	if p.Allow(Suggestion{DedupKey: "b"}, t0.Add(30*time.Second), false) {
		t.Fatal("different key but within min-gap must be blocked")
	}
	if !p.Allow(Suggestion{DedupKey: "b"}, t0.Add(3*time.Minute), false) {
		t.Fatal("different key after min-gap should be allowed")
	}
}
