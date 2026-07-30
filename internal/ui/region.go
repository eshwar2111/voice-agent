// internal/ui/region.go
package ui

import (
	"log"
	"sync"

	"github.com/lxn/win"
)

// physRect is a rounded rectangle in PHYSICAL pixels, relative to the window's
// top-left — exactly what CreateRoundRectRgn consumes.
type physRect struct {
	X, Y, W, H int32
	R          int32 // corner radius
}

// regionShapes converts published CSS-pixel rects into physical-pixel rounded
// rects for CreateRoundRectRgn.
//
// inflate grows every shape by `inflate` CSS px on all four sides. It absorbs
// DPI rounding so the region is never a hair smaller than what CSS painted,
// which would show as a clipped edge on the island.
//
// Degenerate rects are dropped rather than emitted: a zero-area region would
// make the entire UI invisible, so the caller must be able to detect "nothing
// to apply" by getting an empty slice back.
func regionShapes(rects []Rect, radius, inflate, scale float64) []physRect {
	out := make([]physRect, 0, len(rects))
	for _, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			continue
		}
		x := (r.X - inflate) * scale
		y := (r.Y - inflate) * scale
		w := (r.W + 2*inflate) * scale
		h := (r.H + 2*inflate) * scale
		// Region coordinates are window-relative and must never go negative,
		// or the shape silently loses its left/top edge.
		if x < 0 {
			w += x
			x = 0
		}
		if y < 0 {
			h += y
			y = 0
		}
		if w <= 0 || h <= 0 {
			continue
		}
		out = append(out, physRect{
			X: int32(x), Y: int32(y), W: int32(w), H: int32(h),
			R: int32((radius + inflate) * scale),
		})
	}
	return out
}

// regionApplier owns the window's current shape.
type regionApplier struct {
	mu   sync.Mutex
	hwnd win.HWND
}

// Apply clips the window to the union of shapes.
//
// Refuses an empty shape list and leaves the previous region in place: with a
// region-shaped window, an empty region means the UI vanishes entirely, which
// is a far worse failure than anything it could be guarding against.
func (ra *regionApplier) Apply(shapes []physRect) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	if ra.hwnd == 0 {
		return
	}
	if len(shapes) == 0 {
		log.Printf("[ui/region] refusing empty region — keeping previous shape")
		return
	}

	var combined win.HRGN
	for _, s := range shapes {
		hrgn, _, _ := procCreateRoundRectRgn.Call(
			uintptr(s.X), uintptr(s.Y),
			uintptr(s.X+s.W+1), uintptr(s.Y+s.H+1),
			uintptr(s.R*2), uintptr(s.R*2))
		if hrgn == 0 {
			continue
		}
		if combined == 0 {
			combined = win.HRGN(hrgn)
			continue
		}
		win.CombineRgn(combined, combined, win.HRGN(hrgn), win.RGN_OR)
		win.DeleteObject(win.HGDIOBJ(hrgn))
	}
	if combined == 0 {
		log.Printf("[ui/region] all CreateRoundRectRgn calls failed — keeping previous shape")
		return
	}

	// SetWindowRgn takes ownership of the region on success; do not delete it.
	procSetWindowRgn.Call(uintptr(ra.hwnd), uintptr(combined), 1)
}
