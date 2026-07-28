// internal/ui/hittest_test.go
package ui

import "testing"

func TestHit(t *testing.T) {
	island := Rect{X: 800, Y: 10, W: 260, H: 40} // a compact island at 1920 wide

	cases := []struct {
		name  string
		rects []Rect
		p     Point
		scale float64
		want  bool
	}{
		{"inside at 100%", []Rect{island}, Point{900, 20}, 1.0, true},
		{"outside left at 100%", []Rect{island}, Point{700, 20}, 1.0, false},
		{"outside below at 100%", []Rect{island}, Point{900, 60}, 1.0, false},

		// At 125% the island's physical box is x:1000-1325, y:12.5-62.5.
		{"inside at 125%", []Rect{island}, Point{1100, 30}, 1.25, true},
		{"CSS-inside but physically outside at 125%", []Rect{island}, Point{900, 20}, 1.25, false},
		{"inside at 150%", []Rect{island}, Point{1300, 30}, 1.5, true},

		// Boundaries: left/top edges are inclusive, right/bottom exclusive.
		{"left edge inclusive", []Rect{island}, Point{800, 20}, 1.0, true},
		{"top edge inclusive", []Rect{island}, Point{900, 10}, 1.0, true},
		{"right edge exclusive", []Rect{island}, Point{1060, 20}, 1.0, false},
		{"bottom edge exclusive", []Rect{island}, Point{900, 50}, 1.0, false},

		{"empty registry never hits", nil, Point{900, 20}, 1.0, false},
		{"second of two rects", []Rect{island, {X: 0, Y: 900, W: 300, H: 100}},
			Point{100, 950}, 1.0, true},
		{"gap between two rects", []Rect{island, {X: 0, Y: 900, W: 300, H: 100}},
			Point{100, 500}, 1.0, false},
	}

	for _, tc := range cases {
		if got := hit(tc.rects, tc.p, tc.scale); got != tc.want {
			t.Errorf("%s: hit(%v, %v, %v) = %v, want %v",
				tc.name, tc.rects, tc.p, tc.scale, got, tc.want)
		}
	}
}

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
	if !hit(got, Point{960, 100}, 1.0) {
		t.Errorf("fallback does not cover the island's own position")
	}
}

func TestRectRegistrySetReplaces(t *testing.T) {
	reg := newRectRegistry(1920)
	reg.Set([]Rect{{X: 0, Y: 0, W: 10, H: 10}})

	got := reg.Get()
	if len(got) != 1 || got[0].W != 10 {
		t.Fatalf("Get after Set = %v, want the rect that was set", got)
	}
	if hit(got, Point{960, 100}, 1.0) {
		t.Errorf("fallback still active after Set")
	}

	// Setting an empty slice must return to the fallback, not to a dead overlay.
	reg.Set(nil)
	if len(reg.Get()) != 1 || reg.Get()[0].W < 720 {
		t.Errorf("Set(nil) did not restore the fallback")
	}
}
