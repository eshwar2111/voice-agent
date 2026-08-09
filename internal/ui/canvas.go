// internal/ui/canvas.go
package ui

import (
	"log"

	"github.com/lxn/win"
	webview "github.com/webview/webview_go"
)

// Fixed window size in CSS pixels. 1200x800 is the smallest box that contains
// the largest surface (Control Center, 1160x760).
const (
	canvasW = 1200.0
	canvasH = 800.0
)

var canvasCSSWidth, canvasCSSHeight float64 = canvasW, canvasH

// canvas owns the single transparent WebView window.
//
// The window is created once and NEVER resized — resizing forces a WebView2
// relayout, which is the jank this design exists to avoid. It may be MOVED
// (SetWindowPos with SWP_NOSIZE), which does not relayout.
//
// Its visible shape comes from SetWindowRgn, not from its size. Pixels outside
// the region are not part of the window, so clicks reach the desktop by
// definition — there is no click-through flag involved.
type canvas struct {
	w      webview.WebView
	hwnd   win.HWND
	reg    *rectRegistry
	region *regionApplier
	scale  float64
}

func newCanvas(w webview.WebView) *canvas {
	return &canvas{w: w, region: &regionApplier{}}
}

// Attach applies window styles and geometry. Must run on the WebView thread.
func (c *canvas) Attach() {
	c.hwnd = win.HWND(c.w.Window())
	hwndGlobal = c.hwnd
	c.region.hwnd = c.hwnd

	style := win.GetWindowLong(c.hwnd, win.GWL_STYLE)
	win.SetWindowLong(c.hwnd, win.GWL_STYLE, style&^(win.WS_CAPTION|win.WS_THICKFRAME))

	ex := win.GetWindowLong(c.hwnd, win.GWL_EXSTYLE)
	win.SetWindowLong(c.hwnd, win.GWL_EXSTYLE,
		ex|win.WS_EX_TOPMOST|win.WS_EX_TOOLWINDOW|win.WS_EX_NOACTIVATE)

	c.scale = dpiScale()
	pw := int32(canvasW * c.scale)
	ph := int32(canvasH * c.scale)
	sw := win.GetSystemMetrics(win.SM_CXSCREEN)
	sh := win.GetSystemMetrics(win.SM_CYSCREEN)

	// Fix-round-2 (I4): at 150% DPI on a 1080-tall display, ph = 800*1.5 =
	// 1200 physical, anchored at y=0 — the bottom ~120 physical px (including
	// the Control Center's Close/Quit buttons, controlcenter.css puts the
	// dashboard at CSS y 20-780) fall below the bottom of the screen and are
	// permanently unreachable, not just clipped. Clamping ph (and pw, for the
	// same reason, though width wasn't the reported symptom) to the screen's
	// metrics keeps the window itself on-screen. This does NOT by itself make
	// every control reachable — see controlcenter.css's own comment — but an
	// off-screen WINDOW can never be fixed by CSS alone, so this half has to
	// live here. sw/sh use SM_CXSCREEN/SM_CYSCREEN (full screen, matching the
	// existing width-centering code just above) rather than SPI_GETWORKAREA
	// (excludes the taskbar) — not wrapped by github.com/lxn/win, and a
	// window that just touches the taskbar strip is a much smaller problem
	// than one whose controls are off-screen entirely.
	if ph > sh {
		ph = sh
	}
	if pw > sw {
		pw = sw
	}
	x := (sw - pw) / 2

	win.SetWindowPos(c.hwnd, win.HWND_TOPMOST, x, 0, pw, ph,
		win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)

	// Publish the CSS size of the window we ACTUALLY created, not the nominal
	// 1200x800. When the clamp above fires — 200% DPI on a 1080-tall display
	// gives a 960x540 CSS viewport — everything downstream that centres against
	// this value would otherwise be wrong by half the difference: pairLayout
	// would put the island's centre at CSS x=600 in a viewport whose centre is
	// 480, so the island would sit 120px right of centre, and the region
	// registry's fallback rect would be built for a window that does not exist.
	canvasCSSWidth = float64(pw) / c.scale
	canvasCSSHeight = float64(ph) / c.scale

	c.reg = newRectRegistry(canvasCSSWidth)
	// Apply the fallback region immediately so the window has a shape before JS
	// publishes anything. Without this the whole 1200x800 box is clickable.
	c.applyRegion()

	log.Printf("[ui/canvas] fixed %.0fx%.0f css (%dx%d physical) at x=%d, dpiScale=%.2f",
		canvasW, canvasH, pw, ph, x, c.scale)
}

// SetRects records the currently visible surface rects and reshapes the window.
// The slice from Get() is read-only — never sort or mutate it in place.
func (c *canvas) SetRects(rects []Rect) {
	if c.reg == nil {
		return
	}
	c.reg.Set(rects)
	c.applyRegion()
}

func (c *canvas) applyRegion() {
	const (
		regionRadius  = 26.0 // largest island radius; over-rounding is invisible
		regionInflate = 2.0  // absorbs DPI rounding so no painted edge is clipped
	)
	shapes := regionShapes(c.reg.Get(), regionRadius, regionInflate, c.scale)
	c.region.Apply(shapes)
}
