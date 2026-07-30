// internal/ui/region_test.go
package ui

import "testing"

func TestRegionShapes(t *testing.T) {
	island := Rect{X: 470, Y: 10, W: 260, H: 40} // compact island in a 1200-wide window

	cases := []struct {
		name    string
		rects   []Rect
		radius  float64
		inflate float64
		scale   float64
		want    []physRect
	}{
		{
			name: "single rect at 100% with no inflation",
			rects: []Rect{island}, radius: 20, inflate: 0, scale: 1.0,
			want: []physRect{{X: 470, Y: 10, W: 260, H: 40, R: 20}},
		},
		{
			// Inflation grows the shape in all four directions, so origin moves
			// back by `inflate` and size grows by 2*inflate.
			name: "2px inflation at 100%",
			rects: []Rect{island}, radius: 20, inflate: 2, scale: 1.0,
			want: []physRect{{X: 468, Y: 8, W: 264, H: 44, R: 22}},
		},
		{
			name: "125% scale applies to position, size and radius",
			rects: []Rect{island}, radius: 20, inflate: 0, scale: 1.25,
			want: []physRect{{X: 587, Y: 12, W: 325, H: 50, R: 25}},
		},
		{
			name: "150% scale",
			rects: []Rect{island}, radius: 20, inflate: 0, scale: 1.5,
			want: []physRect{{X: 705, Y: 15, W: 390, H: 60, R: 30}},
		},
		{
			name: "two rects both converted",
			rects: []Rect{island, {X: 0, Y: 600, W: 400, H: 150}},
			radius: 20, inflate: 0, scale: 1.0,
			want: []physRect{
				{X: 470, Y: 10, W: 260, H: 40, R: 20},
				{X: 0, Y: 600, W: 400, H: 150, R: 20},
			},
		},
		{
			name: "zero-area rects are dropped, not emitted",
			rects: []Rect{island, {X: 5, Y: 5, W: 0, H: 40}, {X: 5, Y: 5, W: 100, H: 0}},
			radius: 20, inflate: 0, scale: 1.0,
			want: []physRect{{X: 470, Y: 10, W: 260, H: 40, R: 20}},
		},
		{
			name: "no rects yields no shapes",
			rects: nil, radius: 20, inflate: 2, scale: 1.0,
			want: []physRect{},
		},
	}

	for _, tc := range cases {
		got := regionShapes(tc.rects, tc.radius, tc.inflate, tc.scale)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %d shapes, want %d (%v)", tc.name, len(got), len(tc.want), got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: shape %d = %+v, want %+v", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// A region of zero area would make the whole UI invisible — worse than any
// click-through bug. regionShapes must never emit a degenerate shape list that
// the caller could apply blindly.
func TestRegionShapesNeverEmitsZeroArea(t *testing.T) {
	got := regionShapes([]Rect{{X: 10, Y: 10, W: 0, H: 0}}, 20, 0, 1.0)
	if len(got) != 0 {
		t.Fatalf("degenerate rect produced %d shapes, want 0 so the caller can refuse", len(got))
	}
	for _, s := range got {
		if s.W <= 0 || s.H <= 0 {
			t.Errorf("emitted zero-area shape %+v", s)
		}
	}
}

// Inflation must not be able to invert a rect into negative territory.
func TestRegionShapesInflationClampsAtWindowEdge(t *testing.T) {
	got := regionShapes([]Rect{{X: 0, Y: 0, W: 100, H: 40}}, 10, 6, 1.0)
	if len(got) != 1 {
		t.Fatalf("got %d shapes, want 1", len(got))
	}
	if got[0].X < 0 || got[0].Y < 0 {
		t.Errorf("inflation pushed origin negative: %+v — region coords are window-relative "+
			"and must never be negative", got[0])
	}
}
