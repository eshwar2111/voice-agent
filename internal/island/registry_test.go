// internal/island/registry_test.go
package island

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time      { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestRegistry() (*Registry, *fakeClock, *[][]Activity) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	var pushes [][]Activity
	r := NewRegistry(clk, func(as []Activity) {
		cp := make([]Activity, len(as))
		copy(cp, as)
		pushes = append(pushes, cp)
	})
	return r, clk, &pushes
}

func act(id string, prio int, started time.Time) Activity {
	return Activity{ID: id, Kind: "test", Priority: prio, Started: started}
}

func TestSnapshotOrdersByPriorityDescending(t *testing.T) {
	r, clk, _ := newTestRegistry()
	r.Upsert(act("low", 10, clk.t))
	r.Upsert(act("high", 90, clk.t))
	r.Upsert(act("mid", 50, clk.t))

	got := r.Snapshot()
	want := []string{"high", "mid", "low"}
	if len(got) != 3 {
		t.Fatalf("got %d activities, want 3", len(got))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestSnapshotTiesAreStableByInsertionOrder(t *testing.T) {
	r, clk, _ := newTestRegistry()
	r.Upsert(act("first", 50, clk.t))
	r.Upsert(act("second", 50, clk.t))

	got := r.Snapshot()
	if got[0].ID != "first" || got[1].ID != "second" {
		t.Errorf("tie order = %q,%q — want first,second (stable insertion order, "+
			"so equal-priority activities don't flicker between slots)", got[0].ID, got[1].ID)
	}
}

func TestUpsertReplacesSameID(t *testing.T) {
	r, clk, _ := newTestRegistry()
	a := act("timer", 50, clk.t)
	a.Data = map[string]any{"remaining": 60}
	r.Upsert(a)
	a.Data = map[string]any{"remaining": 59}
	r.Upsert(a)

	got := r.Snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d activities, want 1 (same ID must replace, not append)", len(got))
	}
	if got[0].Data["remaining"] != 59 {
		t.Errorf("Data not replaced: got %v", got[0].Data)
	}
}

func TestEndRemoves(t *testing.T) {
	r, clk, _ := newTestRegistry()
	r.Upsert(act("timer", 50, clk.t))
	r.End("timer")
	if len(r.Snapshot()) != 0 {
		t.Errorf("End did not remove the activity")
	}
	r.End("never-existed") // must not panic
}

// Dismissal is keyed on ID + Started. An update to a still-live instance must
// NOT resurrect it, or dismissing a per-second timer would be useless.
func TestDismissSurvivesUpdatesToSameInstance(t *testing.T) {
	r, clk, _ := newTestRegistry()
	started := clk.t
	r.Upsert(act("timer", 50, started))
	r.Dismiss("timer")

	if len(r.Snapshot()) != 0 {
		t.Fatalf("dismissed activity still visible")
	}

	a := act("timer", 50, started)
	a.Data = map[string]any{"remaining": 42}
	r.Upsert(a) // same Started — still the same instance
	if len(r.Snapshot()) != 0 {
		t.Errorf("an update to a dismissed instance resurrected it")
	}
}

func TestDismissClearsForGenuinelyNewInstance(t *testing.T) {
	r, clk, _ := newTestRegistry()
	started := clk.t
	r.Upsert(act("timer", 50, started))
	r.Dismiss("timer")

	clk.advance(time.Minute)
	r.Upsert(act("timer", 50, clk.t)) // new Started = new instance

	if len(r.Snapshot()) != 1 {
		t.Errorf("a new instance did not clear the dismissal")
	}
}

func TestCapDropsNewActivitiesButAllowsUpdates(t *testing.T) {
	r, clk, _ := newTestRegistry()
	for i := 0; i < MaxLive; i++ {
		r.Upsert(Activity{ID: string(rune('a' + i)), Priority: 1, Started: clk.t})
	}
	if len(r.Snapshot()) != MaxLive {
		t.Fatalf("got %d, want %d", len(r.Snapshot()), MaxLive)
	}

	r.Upsert(Activity{ID: "overflow", Priority: 99, Started: clk.t})
	if len(r.Snapshot()) != MaxLive {
		t.Errorf("cap exceeded: a runaway provider emitting unique IDs must not grow the list")
	}

	// An update to an ALREADY-LIVE activity must still be accepted at the cap,
	// otherwise a full registry would freeze every activity's data.
	upd := Activity{ID: "a", Priority: 1, Started: clk.t, Data: map[string]any{"x": 1}}
	r.Upsert(upd)
	for _, a := range r.Snapshot() {
		if a.ID == "a" && a.Data["x"] == 1 {
			return
		}
	}
	t.Errorf("update to an existing activity was rejected at the cap")
}
