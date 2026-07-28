package ui

import "sync"

// Rect is an interactive region in CSS pixels, relative to the canvas
// window's top-left corner. JS publishes these via setHitRects.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Point is a cursor position in physical pixels, relative to the canvas
// window's top-left corner.
type Point struct {
	X int32
	Y int32
}

// hit reports whether p falls inside any rect. Rects are CSS pixels and p is
// physical pixels, so every rect is scaled by the window's DPI factor first.
// Left/top edges are inclusive, right/bottom exclusive — so adjacent rects
// never both claim the same pixel.
func hit(rects []Rect, p Point, scale float64) bool {
	px, py := float64(p.X), float64(p.Y)
	for _, r := range rects {
		x0, y0 := r.X*scale, r.Y*scale
		if px >= x0 && px < x0+r.W*scale && py >= y0 && py < y0+r.H*scale {
			return true
		}
	}
	return false
}

// rectRegistry holds the interactive regions JS most recently published.
// It is read by the cursor loop (~60Hz) and written by the WebView thread.
type rectRegistry struct {
	mu       sync.RWMutex
	rects    []Rect
	fallback Rect
}

// newRectRegistry builds a registry for a canvas cssWidth CSS pixels wide.
//
// The fallback matters more than it looks: if JS never publishes rects (crash
// during load, script error), we must still leave the island clickable while
// keeping the rest of the screen click-through. It covers the island's largest
// presence — sheet, 720x520 — centered and top-anchored. The failure mode is
// never "invisible window eats the whole desktop".
func newRectRegistry(cssWidth float64) *rectRegistry {
	const sheetW, sheetH = 720.0, 520.0
	return &rectRegistry{
		fallback: Rect{X: cssWidth/2 - sheetW/2, Y: 0, W: sheetW, H: sheetH},
	}
}

func (r *rectRegistry) Set(rects []Rect) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rects = rects
}

func (r *rectRegistry) Get() []Rect {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.rects) == 0 {
		return []Rect{r.fallback}
	}
	return r.rects
}
