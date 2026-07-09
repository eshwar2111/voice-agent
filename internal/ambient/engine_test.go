package ambient

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recDeliverer struct {
	mu   sync.Mutex
	last string
}

func (r *recDeliverer) ShowSuggestion(id string, s Suggestion) {
	r.mu.Lock()
	r.last = id + ":" + s.Title
	r.mu.Unlock()
}
func (r *recDeliverer) lastShown() string { r.mu.Lock(); defer r.mu.Unlock(); return r.last }

func newTestEngine(ui Deliverer, busy bool) *Engine {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	return &Engine{
		Policy:  NewPolicy(time.Minute),
		UI:      ui,
		Busy:    func() bool { return busy },
		Enabled: func() bool { return true },
		Now:     func() time.Time { return t0 },
	}
}

func TestEngineDeliversThenSuppressesDuplicate(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, false)
	s := Suggestion{Title: "Unzip?", DedupKey: "zip1"}
	e.consider(s)
	if ui.lastShown() == "" {
		t.Fatal("first suggestion should be delivered")
	}
	// while one is active, a second is dropped (one-at-a-time)
	before := ui.lastShown()
	e.consider(Suggestion{Title: "Other", DedupKey: "x"})
	if ui.lastShown() != before {
		t.Fatal("second suggestion must be dropped while one is active")
	}
}

func TestEngineSuppressedWhenBusy(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, true) // busy
	e.consider(Suggestion{Title: "x", DedupKey: "k"})
	if ui.lastShown() != "" {
		t.Fatal("nothing should be delivered while busy")
	}
}

func TestEngineAcceptRunsAction(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, false)
	done := make(chan struct{})
	e.consider(Suggestion{Title: "Do", DedupKey: "k", Run: func(context.Context) error { close(done); return nil }})
	// the delivered id is "sg1"
	e.Accept("sg1")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Accept should run the action")
	}
}
