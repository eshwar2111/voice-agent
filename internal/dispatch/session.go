package dispatch

import (
	"strings"
	"sync"
	"time"
)

// taskSession is TaskSession v1: a lightweight, in-memory record of the ongoing
// task so a follow-up turn (spoken right after, via mic-hot continuity)
// continues the SAME task with context instead of being planned cold — e.g.
// "open my budget" … "email it to Priya", or "create a friday event" … "make it
// 3pm". It is NOT a chat log: only a compact rolling window of recent user
// turns, injected into Tier-1 planning to resolve references ("it", "that", "the
// second one"). It expires after inactivity, so a later unrelated command starts
// a fresh task rather than dragging stale context along.
type taskSession struct {
	mu      sync.Mutex
	turns   []string
	updated time.Time
}

const (
	sessionTTL   = 90 * time.Second // idle gap that ends a task
	maxTaskTurns = 4                // rolling context depth
)

// contextIfActive returns the ongoing-task context block, or "" if there is no
// active (non-expired) task. Reading also expires a stale session.
func (s *taskSession) contextIfActive(now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.turns) == 0 || now.Sub(s.updated) > sessionTTL {
		s.turns = nil
		return ""
	}
	return "Ongoing conversation so far (oldest first) — use it to resolve references " +
		"like \"it\", \"that\", or \"the second one\"; if this request is unrelated, ignore it:\n" +
		strings.Join(s.turns, "\n")
}

// record appends the user's turn to the rolling window, starting a fresh task if
// the previous one had gone idle.
func (s *taskSession) record(input string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.updated) > sessionTTL {
		s.turns = nil // stale — this begins a new task
	}
	s.turns = append(s.turns, "User: "+strings.TrimSpace(input))
	if len(s.turns) > maxTaskTurns {
		s.turns = s.turns[len(s.turns)-maxTaskTurns:]
	}
	s.updated = now
}
