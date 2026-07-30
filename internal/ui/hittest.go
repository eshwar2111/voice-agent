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

// rectRegistry holds the interactive regions JS most recently published.
// It is written by the WebView thread (via the setRegionRects binding) and
// read back on the same thread when canvas.applyRegion builds the window
// region — there is no separate polling reader.
type rectRegistry struct {
	mu       sync.RWMutex
	rects    []Rect
	fallback Rect
}

// newRectRegistry builds a registry for a canvas cssWidth CSS pixels wide.
//
// The fallback matters more here than it looks: the window is clipped to the
// region built from these rects, so an empty registry would clip the UI away
// entirely. It covers the island's largest presence — sheet, 720x520 —
// centered and top-anchored, so a JS failure leaves a usable window rather
// than an invisible one.
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
