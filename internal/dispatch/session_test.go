package dispatch

import (
	"strings"
	"testing"
	"time"
)

func TestTaskSessionCrossTurnContext(t *testing.T) {
	var s taskSession
	t0 := time.Now()
	// No task yet -> no context.
	if c := s.contextIfActive(t0); c != "" {
		t.Fatalf("expected empty context initially, got %q", c)
	}
	s.record("open my budget", t0)
	// A follow-up shortly after sees the prior turn.
	ctx := s.contextIfActive(t0.Add(3 * time.Second))
	if !strings.Contains(ctx, "open my budget") {
		t.Fatalf("follow-up should see prior turn, got %q", ctx)
	}
	s.record("email it to Priya", t0.Add(3*time.Second))
	ctx = s.contextIfActive(t0.Add(4 * time.Second))
	if !strings.Contains(ctx, "budget") || !strings.Contains(ctx, "Priya") {
		t.Fatalf("both turns should be in context, got %q", ctx)
	}
}

func TestTaskSessionExpires(t *testing.T) {
	var s taskSession
	t0 := time.Now()
	s.record("open my budget", t0)
	// After the TTL, the task is stale -> no context (fresh task).
	if c := s.contextIfActive(t0.Add(sessionTTL + time.Second)); c != "" {
		t.Fatalf("stale session must yield empty context, got %q", c)
	}
	// And recording after the gap starts fresh (no old turn).
	s.record("what's the weather", t0.Add(sessionTTL+2*time.Second))
	ctx := s.contextIfActive(t0.Add(sessionTTL + 3*time.Second))
	if strings.Contains(ctx, "budget") {
		t.Fatalf("new task must not carry stale 'budget', got %q", ctx)
	}
}

func TestTaskSessionRollingWindow(t *testing.T) {
	var s taskSession
	now := time.Now()
	for _, in := range []string{"cmd1", "cmd2", "cmd3", "cmd4", "cmd5", "cmd6"} {
		s.record(in, now)
		now = now.Add(time.Second)
	}
	ctx := s.contextIfActive(now)
	if strings.Contains(ctx, "cmd1") || strings.Contains(ctx, "cmd2") {
		t.Fatalf("window should drop oldest turns, got %q", ctx)
	}
	if !strings.Contains(ctx, "cmd6") {
		t.Fatalf("window should keep newest, got %q", ctx)
	}
}
