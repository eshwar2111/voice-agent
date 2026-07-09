package ambient

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Engine fans in all sources, applies Policy, and delivers one suggestion at a time.
type Engine struct {
	Sources []Source
	Policy  *Policy
	UI      Deliverer
	Busy    func() bool      // suppress while the assistant is busy (nil => not busy)
	Enabled func() bool      // master toggle + privacy (nil => enabled)
	Now     func() time.Time // injectable clock (nil => time.Now)

	mu       sync.Mutex
	active   *Suggestion
	activeID string
	counter  int
}

func (e *Engine) clock() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Run starts every source and delivers policy-approved suggestions until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	out := make(chan Suggestion, 16)
	for _, s := range e.Sources {
		go s.Start(ctx, out)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-out:
			e.consider(s)
		}
	}
}

func (e *Engine) consider(s Suggestion) {
	if e.Enabled != nil && !e.Enabled() {
		return
	}
	busy := e.Busy != nil && e.Busy()
	now := e.clock()

	e.mu.Lock()
	if e.active != nil { // one at a time
		e.mu.Unlock()
		return
	}
	if !e.Policy.Allow(s, now, busy) {
		e.mu.Unlock()
		return
	}
	e.counter++
	id := fmt.Sprintf("sg%d", e.counter)
	sc := s
	e.active = &sc
	e.activeID = id
	e.Policy.Record(s, now)
	e.mu.Unlock()

	e.UI.ShowSuggestion(id, s)
}

// Accept runs the active suggestion's action if id matches, then clears it.
func (e *Engine) Accept(id string) {
	e.mu.Lock()
	s := e.active
	match := e.activeID == id
	if match {
		e.active = nil
		e.activeID = ""
	}
	e.mu.Unlock()
	if match && s != nil && s.Run != nil {
		go func() { _ = s.Run(context.Background()) }()
	}
}

// Dismiss clears the active suggestion if id matches (also called by the UI 15s timeout).
func (e *Engine) Dismiss(id string) {
	e.mu.Lock()
	if e.activeID == id {
		e.active = nil
		e.activeID = ""
	}
	e.mu.Unlock()
}
