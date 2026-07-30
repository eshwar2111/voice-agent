// internal/ui/hittest_test.go
package ui

import "testing"

func TestRectRegistryFallsBackWhenEmpty(t *testing.T) {
	reg := newRectRegistry(1920)

	got := reg.Get()
	if len(got) != 1 {
		t.Fatalf("empty registry returned %d rects, want 1 fallback", len(got))
	}
	// Fallback must keep the island clickable: centered, top-anchored, large
	// enough for the biggest island presence (sheet, 720x520).
	f := got[0]
	if f.W < 720 || f.H < 520 {
		t.Errorf("fallback %v too small to cover the sheet presence", f)
	}
	if f.X+f.W/2 != 960 {
		t.Errorf("fallback %v is not horizontally centered in 1920", f)
	}
}

func TestRectRegistrySetReplaces(t *testing.T) {
	reg := newRectRegistry(1920)
	reg.Set([]Rect{{X: 0, Y: 0, W: 10, H: 10}})

	got := reg.Get()
	if len(got) != 1 || got[0].W != 10 {
		t.Fatalf("Get after Set = %v, want the rect that was set", got)
	}

	// Setting an empty slice must return to the fallback, not to a dead overlay.
	reg.Set(nil)
	if len(reg.Get()) != 1 || reg.Get()[0].W < 720 {
		t.Errorf("Set(nil) did not restore the fallback")
	}
}
