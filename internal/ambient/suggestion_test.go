package ambient

import (
	"context"
	"testing"
)

func TestSuggestionRunInvokes(t *testing.T) {
	called := false
	s := Suggestion{Title: "t", DedupKey: "k", Run: func(context.Context) error { called = true; return nil }}
	_ = s.Run(context.Background())
	if !called {
		t.Fatal("Run should invoke the closure")
	}
	// interface satisfaction check (compile-time via var)
	var _ Deliverer = fakeDeliverer{}
}

type fakeDeliverer struct{ last Suggestion; id string }

func (f fakeDeliverer) ShowSuggestion(id string, s Suggestion) {}
