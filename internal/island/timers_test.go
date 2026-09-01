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

func TestTimersPauseFreezesRemaining(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	tm := NewTimers(clk)
	tm.Add("t1", "tea", clk.t.Add(90*time.Second))

	clk.advance(30 * time.Second) // 60s left
	tm.Pause("t1")

	got := tm.snapshot()[0]
	if got.Data["remaining"] != 60 {
		t.Fatalf("remaining at pause = %v, want 60", got.Data["remaining"])
	}
	if got.Data["paused"] != true {
		t.Errorf("paused flag = %v, want true", got.Data["paused"])
	}

	// Time keeps moving, but a paused timer must not drain.
	clk.advance(45 * time.Second)
	if got := tm.snapshot()[0].Data["remaining"]; got != 60 {
		t.Errorf("paused timer drained: remaining = %v, want frozen 60", got)
	}
	// A held timer must never wake the island, even if it happens to sit at 0.
	if tm.snapshot()[0].Significant {
		t.Errorf("a paused timer must not be Significant")
	}
	// total is the original duration, unaffected by the pause.
	if got := tm.snapshot()[0].Data["total"]; got != 90 {
		t.Errorf("total = %v, want 90 (unchanged by pause)", got)
	}

	// Resume: it drains again from the frozen point, not from wall time.
	tm.Resume("t1")
	clk.advance(10 * time.Second)
	after := tm.snapshot()[0]
	if after.Data["remaining"] != 50 {
		t.Errorf("after resume+10s remaining = %v, want 50", after.Data["remaining"])
	}
	if after.Data["paused"] != false {
		t.Errorf("paused flag after resume = %v, want false", after.Data["paused"])
	}
	if after.Data["total"] != 90 {
		t.Errorf("total after resume = %v, want 90 (ring must not jump)", after.Data["total"])
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
