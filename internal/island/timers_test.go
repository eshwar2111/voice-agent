package island

import (
	"testing"
	"time"
)

func TestTimersSnapshotProducesActivity(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(90*time.Second))

	got := tm.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d activities, want 1", len(got))
	}
	a := got[0]
	if a.ID != "timer.t1" {
		t.Errorf("ID = %q, want timer.t1", a.ID)
	}
	if a.Kind != "timer" {
		t.Errorf("Kind = %q, want timer", a.Kind)
	}
	if a.Data["label"] != "tea" {
		t.Errorf("label = %v, want tea", a.Data["label"])
	}
	if a.Data["remaining"] != 90 {
		t.Errorf("remaining = %v, want 90 (seconds)", a.Data["remaining"])
	}
	if a.Significant {
		t.Errorf("a routine tick must NOT be Significant, or the island twitches every second")
	}
}

func TestTimersMarksZeroSignificant(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(2*time.Second))

	clk.advance(2 * time.Second)
	got := tm.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d activities, want 1", len(got))
	}
	if got[0].Data["remaining"] != 0 {
		t.Errorf("remaining = %v, want 0", got[0].Data["remaining"])
	}
	if !got[0].Significant {
		t.Errorf("reaching zero MUST be Significant — it is the update that matters most")
	}
}

func TestTimersRemainingNeverNegative(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(time.Second))

	clk.advance(30 * time.Second) // long overdue
	got := tm.snapshot()
	if got[0].Data["remaining"] != 0 {
		t.Errorf("remaining = %v, want 0 — a countdown must never render negative", got[0].Data["remaining"])
	}
}

func TestTimersRemoveDropsIt(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(time.Minute))
	tm.Remove("t1")
	if len(tm.snapshot()) != 0 {
		t.Errorf("Remove did not drop the timer")
	}
}

// Started must be stable across ticks, or dismissal (keyed on ID+Started)
// would clear itself every second.
func TestTimersStartedIsStableAcrossTicks(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(time.Minute))

	first := tm.snapshot()[0].Started
	clk.advance(3 * time.Second)
	second := tm.snapshot()[0].Started

	if !first.Equal(second) {
		t.Errorf("Started changed between ticks (%v -> %v) — dismissal would reset every tick", first, second)
	}
}
