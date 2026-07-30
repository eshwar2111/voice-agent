# SP5 Dynamic Island Shell — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the resize-driven WebView overlay with a Dynamic Island that morphs fluidly between five sizes, reacts to hover, and swells on its own when the agent or an integration has something to say.

**Architecture:** One transparent WebView window at a fixed 1200×800, created once and never resized (it may be *moved*, which does not relayout WebView2). All shape changes are CSS animations inside it. `SetWindowRgn` clips the window to the shape of whatever is visible, so pixels outside are not the window and clicks reach the desktop by definition — no click-through flag is involved. Go→JS becomes a single typed JSON envelope instead of string-interpolated `w.Eval` calls.

> **Revised 2026-07-29.** Task 0's spike FAILED: `WS_EX_TRANSPARENT` did not make a WebView2-hosted window click-through. The plan moved to region-shaped windows. Tasks 1 and 2 are already complete and survive the change (Task 2 partially — see Task 3). Task 0 is closed.

**Tech Stack:** Go 1.26, `webview/webview_go` (WebView2), `lxn/win` for Win32, `go:embed` + `net/http` loopback for assets, vanilla ES modules + CSS (no frameworks, no CDN — must work fully offline).

**Spec:** `docs/superpowers/specs/2026-07-29-dynamic-island-shell-design.md`

## Global Constraints

- **No file outside `internal/ui/` changes.** All 8 exported functions keep their current signatures. 30 call sites across 12 files must continue to compile and behave.
- **No new Go dependencies.** No new `require` lines in `go.mod`.
- **No network access at runtime.** No CDN fonts, scripts, or icon fonts. Everything embedded.
- **No config changes.** Nothing added to `config.json` or `internal/config`.
- Tests use stdlib `testing`, table-driven, matching `internal/trust/classify_test.go` style. No testify.
- Build command: `go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app`
- CGO toolchain required. In the Bash tool, prefix Go commands with `export PATH="$PATH:/c/w64devkit/bin"`.
- Test command: `go test ./internal/ui/...`
- All git commit messages end with:
  `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`
- Rect coordinates crossing the JS→Go boundary are **CSS pixels relative to the window's top-left**. Go multiplies by `dpiScale()` to get physical pixels. Never mix the two.
- The window is **never resized** after creation. `SetWindowPos` may only be called with `SWP_NOSIZE`. Resizing forces a WebView2 relayout, which is the jank this whole design exists to avoid.
- A region of zero total area would make the entire UI invisible. Region application must refuse an empty shape list and keep the previous region.

---

### Task 0: Spike — verify `WS_EX_TRANSPARENT` click-through works through WebView2

The entire architecture assumes a transparent full-screen WebView can be made click-through. WebView2 hosts its renderer in a **child HWND**, and `WS_EX_TRANSPARENT` is documented to affect the window it's set on. If hit-testing doesn't skip the child, the approach collapses. Prove it in 40 lines before writing 900.

**Files:**
- Create: `cmd/spike-clickthrough/main.go` (deleted at the end of this task — never committed)
- Modify: `docs/superpowers/plans/2026-07-29-sp5-dynamic-island-shell.md` (record the result)

**Interfaces:**
- Consumes: nothing
- Produces: a go/no-go decision recorded in this plan. Every later task assumes GO.

- [ ] **Step 1: Write the spike**

```go
// cmd/spike-clickthrough/main.go
package main

import (
	"log"
	"os"
	"time"

	"github.com/lxn/win"
	webview "github.com/webview/webview_go"
)

func main() {
	os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "0")
	w := webview.NewWindow(false, nil)
	defer w.Destroy()
	w.SetTitle("spike")
	w.SetSize(800, 600, webview.HintNone)
	// A red box top-left so we can see the window is really there and really on top.
	w.SetHtml(`<body style="margin:0;background:transparent">
	  <div style="width:300px;height:120px;background:rgba(255,0,0,.85);color:#fff;
	              font:16px sans-serif;padding:12px">SPIKE — click me, then click below me</div>
	</body>`)

	go func() {
		time.Sleep(500 * time.Millisecond)
		w.Dispatch(func() {
			hwnd := win.HWND(w.Window())
			style := win.GetWindowLong(hwnd, win.GWL_STYLE)
			win.SetWindowLong(hwnd, win.GWL_STYLE, style&^(win.WS_CAPTION|win.WS_THICKFRAME))
			ex := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
			win.SetWindowLong(hwnd, win.GWL_EXSTYLE,
				ex|win.WS_EX_TOPMOST|win.WS_EX_TOOLWINDOW|win.WS_EX_NOACTIVATE)
			win.SetWindowPos(hwnd, win.HWND_TOPMOST, 0, 0, 800, 600, win.SWP_NOACTIVATE)
		})
		// Toggle WS_EX_TRANSPARENT every 3 seconds and log the state.
		on := false
		for {
			time.Sleep(3 * time.Second)
			on = !on
			state := on
			w.Dispatch(func() {
				hwnd := win.HWND(w.Window())
				ex := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
				if state {
					win.SetWindowLong(hwnd, win.GWL_EXSTYLE, ex|win.WS_EX_TRANSPARENT)
				} else {
					win.SetWindowLong(hwnd, win.GWL_EXSTYLE, ex&^win.WS_EX_TRANSPARENT)
				}
				log.Printf("WS_EX_TRANSPARENT = %v", state)
			})
		}
	}()

	w.Run()
}
```

- [ ] **Step 2: Build and run it**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go run ./cmd/spike-clickthrough
```

Open Notepad (or any window) first so something is visible underneath the spike window.

- [ ] **Step 3: Verify both phases by hand**

Watch the console for `WS_EX_TRANSPARENT = true/false` and test each phase:

| Phase | Click on the red box | Click on empty (transparent) area |
|---|---|---|
| `= false` | Window takes the click (red box is interactive) | Window takes the click — Notepad does **not** get focus |
| `= true` | Click passes through to Notepad | Click passes through to Notepad |

**PASS** requires: during `= true`, clicking anywhere — including directly on the red box — lands on Notepad underneath. That proves hit-testing skips the WebView2 child HWND, not just the parent frame.

- [ ] **Step 4: Record the result in this plan**

Replace this line in the plan file (this task's section) with the outcome:

```markdown
**SPIKE RESULT (fill in):** PASS / FAIL — <one sentence of what was observed>
```

If **FAIL**: stop. Do not proceed to Task 1. The fallback is per-frame `SetWindowRgn` (spec §1, "Fallback if it fails"), which requires re-planning Tasks 2, 3 and part of 5. Report back rather than improvising.

- [ ] **Step 5: Delete the spike and commit the finding**

```bash
rm -rf cmd/spike-clickthrough
git add docs/superpowers/plans/2026-07-29-sp5-dynamic-island-shell.md
git commit -m "docs(sp5): record WS_EX_TRANSPARENT click-through spike result

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

**SPIKE RESULT: FAIL.** v1 flipped `WS_EX_TRANSPARENT` without a following
`SetWindowPos(SWP_FRAMECHANGED)`, so the window never re-read its frame and both phases behaved
identically. v2 fixed that and tested three modes — baseline, `WS_EX_TRANSPARENT`, and
`WS_EX_LAYERED | WS_EX_TRANSPARENT` (with `SetLayeredWindowAttributes`) — each applied with
`SWP_FRAMECHANGED`. Clicks were still swallowed. Conclusion: this flag is not a viable
click-through mechanism for a WebView2-hosted window here.

Switching UI frameworks was considered and rejected: Electron's `setIgnoreMouseEvents`, Tauri's
`set_ignore_cursor_events`, and Wails' `SetIgnoreMouseEvents` all wrap the same Win32 flag.

**Resolution:** adopt the spec's documented fallback — region-shaped windows (spec §1). Clicks
fall through because the pixels are not part of the window, so no flag is involved. `hit()` from
Task 2 becomes dead code and is removed in Task 3. `cmd/spike-clickthrough` is deleted.

---

### Task 1: Embedded asset server

Move the UI out of a Go string literal and onto a loopback HTTP server backed by `go:embed`. This buys real ES modules and separate files, and is the mechanism the widget platform will later use to load widgets. Behavior must be **identical** after this task — it is a pure delivery-mechanism swap.

**Files:**
- Create: `internal/ui/assets.go`
- Create: `internal/ui/assets/index.html` (verbatim copy of `internal/ui/overlay_v2.html`)
- Create: `internal/ui/assets_test.go`
- Modify: `internal/ui/overlay.go:81-82` (drop `//go:embed overlay_v2.html`), `internal/ui/overlay.go:365` (`SetHtml` → `Navigate`)

**Interfaces:**
- Consumes: nothing
- Produces:
  - `func startAssetServer() (*assetServer, error)`
  - `type assetServer struct { URL string; ln net.Listener }` — `URL` ends in `/`, so `srv.URL + "index.html"` is the page
  - `func (s *assetServer) Close() error`

- [ ] **Step 1: Write the failing test**

```go
// internal/ui/assets_test.go
package ui

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAssetServerServesIndex(t *testing.T) {
	srv, err := startAssetServer()
	if err != nil {
		t.Fatalf("startAssetServer: %v", err)
	}
	defer srv.Close()

	if !strings.HasSuffix(srv.URL, "/") {
		t.Fatalf("URL %q must end in /", srv.URL)
	}
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		t.Fatalf("URL %q must be loopback", srv.URL)
	}

	resp, err := http.Get(srv.URL + "index.html")
	if err != nil {
		t.Fatalf("GET index.html: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("index.html status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<!DOCTYPE html>") {
		t.Errorf("index.html does not look like the overlay page")
	}
}

func TestAssetServerRejectsUnprefixedPaths(t *testing.T) {
	srv, err := startAssetServer()
	if err != nil {
		t.Fatalf("startAssetServer: %v", err)
	}
	defer srv.Close()

	// Strip the random prefix — a caller guessing the port must still miss.
	base := srv.URL[:strings.Index(srv.URL[len("http://"):], "/")+len("http://")+1]
	resp, err := http.Get(base + "index.html")
	if err != nil {
		t.Fatalf("GET unprefixed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Errorf("unprefixed path returned 200, want 404")
	}
}

func TestAssetServerPrefixIsRandomPerLaunch(t *testing.T) {
	a, err := startAssetServer()
	if err != nil {
		t.Fatalf("startAssetServer: %v", err)
	}
	defer a.Close()
	b, err := startAssetServer()
	if err != nil {
		t.Fatalf("startAssetServer: %v", err)
	}
	defer b.Close()
	if a.URL == b.URL {
		t.Errorf("two servers produced the same URL %q", a.URL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -run TestAssetServer -v
```

Expected: FAIL — `undefined: startAssetServer`

- [ ] **Step 3: Copy the page into the asset directory**

```bash
mkdir -p internal/ui/assets
cp internal/ui/overlay_v2.html internal/ui/assets/index.html
```

Do not edit the copy in this task. Byte-identical parity is what makes this step safe.

- [ ] **Step 4: Write the implementation**

```go
// internal/ui/assets.go
package ui

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"io/fs"
	"net"
	"net/http"
)

//go:embed assets
var assetFS embed.FS

// assetServer serves the embedded UI over loopback. WebView2 needs a real
// origin (not SetHtml) for ES modules to load, and the widget platform will
// need to serve additional files later.
type assetServer struct {
	URL string // e.g. http://127.0.0.1:52341/9f3a.../  — always ends in "/"
	ln  net.Listener
	srv *http.Server
}

func startAssetServer() (*assetServer, error) {
	sub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		return nil, err
	}

	// Random per-launch path prefix so another local process that guesses the
	// port still can't fetch the UI. The assets aren't secret, but the surface
	// costs nothing to close.
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	prefix := "/" + hex.EncodeToString(buf) + "/"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	return &assetServer{
		URL: "http://" + ln.Addr().String() + prefix,
		ln:  ln,
		srv: srv,
	}, nil
}

func (s *assetServer) Close() error {
	if s == nil || s.srv == nil {
		return nil
	}
	return s.srv.Close()
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -run TestAssetServer -v
```

Expected: PASS (3 tests)

- [ ] **Step 6: Point the WebView at the server**

In `internal/ui/overlay.go`, delete these two lines (currently `:81-82`):

```go
//go:embed overlay_v2.html
var htmlTemplate string
```

Remove the now-unused `_ "embed"` import. Replace `w.SetHtml(htmlTemplate)` (currently `:365`) with:

```go
	assets, err := startAssetServer()
	if err != nil {
		log.Fatalf("[ui] cannot start asset server: %v", err)
	}
	defer assets.Close()
	log.Printf("[ui] assets at %s", assets.URL)
	w.Navigate(assets.URL + "index.html")
```

- [ ] **Step 7: Build and verify parity by hand**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

Verify, comparing against the pre-change behavior: the pill appears top-center; clicking it triggers listen; the gear opens the Control Center; Esc closes it. If any of these regress, the cause is delivery mechanism, not design — fix before committing.

- [ ] **Step 8: Commit**

```bash
git add internal/ui/assets.go internal/ui/assets_test.go internal/ui/assets/index.html internal/ui/overlay.go
git commit -m "feat(ui): serve overlay from embedded loopback asset server

Swaps SetHtml for a go:embed FS served on 127.0.0.1:0 under a random
per-launch path prefix. Enables ES modules and per-file assets. Page
content is byte-identical to overlay_v2.html; behavior unchanged.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Hit-test geometry (pure logic, no Win32)

The cursor-poll loop can't be unit-tested, so all its arithmetic lives in a pure function that can be. This task ships **only** that function and its registry — nothing is wired up yet, so the app is unaffected.

**Files:**
- Create: `internal/ui/hittest.go`
- Create: `internal/ui/hittest_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `type Rect struct { X, Y, W, H float64 }` — CSS px, relative to window top-left
  - `type Point struct { X, Y int32 }` — physical px, relative to window top-left
  - `func hit(rects []Rect, p Point, scale float64) bool`
  - `type rectRegistry struct{...}` with `func newRectRegistry(cssWidth float64) *rectRegistry`, `func (r *rectRegistry) Set(rects []Rect)`, `func (r *rectRegistry) Get() []Rect`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -run "TestHit|TestRectRegistry" -v
```

Expected: FAIL — `undefined: hit`, `undefined: newRectRegistry`

- [ ] **Step 3: Write the implementation**

```go
// internal/ui/hittest.go
package ui

import "sync"

// Rect is an interactive region in CSS pixels, relative to the canvas
// window's top-left corner. JS publishes these via setRegionRects.
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -run "TestHit|TestRectRegistry" -v
```

Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/ui/hittest.go internal/ui/hittest_test.go
git commit -m "feat(ui): hit-test geometry and rect registry

Pure DPI-aware point-in-rects test plus the registry JS publishes into.
Fallback rect keeps the island clickable if JS never reports.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: Fixed-size window + region shaping

Turn the window into a fixed 1200×800 transparent surface clipped by `SetWindowRgn`, and delete the resize machinery. After this task the app looks the same but is structurally different: every surface is positioned by CSS, and the window's *shape* comes from a region rather than its size.

**Files:**
- Create: `internal/ui/canvas.go`
- Create: `internal/ui/region.go`
- Create: `internal/ui/region_test.go`
- Modify: `internal/ui/hittest.go` — delete `hit()` and `Point` (dead under the region design); keep `Rect` and `rectRegistry`
- Modify: `internal/ui/hittest_test.go` — delete `TestHit`; keep both registry tests
- Modify: `internal/ui/overlay.go` — delete `resizeWindow` (`:217-252`), `defaultW`/`defaultH` (`:60-64`), the `callResize` binding (`:328-330`), `procRedrawWindow` and `procGetWindowRect` lazy procs, and the `time.Sleep(250ms)` startup block (`:367-381`). **Keep** `procCreateRoundRectRgn` and `procSetWindowRgn` — `region.go` uses them.
- Modify: `internal/ui/assets/index.html` — surfaces become absolutely positioned; `sizeTo()` publishes region rects instead of resizing

**Interfaces:**
- Consumes: `Rect`, `newRectRegistry` (Task 2); `startAssetServer` (Task 1)
- Produces:
  - `func newCanvas(w webview.WebView) *canvas`
  - `func (c *canvas) Attach()` — creates window geometry and styles; call once on `uiReady`
  - `func (c *canvas) SetRects(rects []Rect)` — publishes rects and applies the region
  - `type physRect struct { X, Y, W, H, R int32 }`
  - `func regionShapes(rects []Rect, radius, inflate, scale float64) []physRect`
  - `var canvasCSSWidth, canvasCSSHeight float64`

**Carried finding from Task 2 (ledger):** `rectRegistry.Get()` returns the internal slice by reference. It is race-safe today only because `Set()` always assigns a new slice header. This task adds the first real consumer — **treat the returned slice as read-only; never sort or mutate it in place.**

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -run TestRegion -v
```

Expected: FAIL — `undefined: regionShapes`, `undefined: physRect`

- [ ] **Step 3: Write `region.go`**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -run TestRegion -v
```

Expected: PASS (3 tests)

- [ ] **Step 5: Remove the dead click-through code**

`hit()` and `Point` in `internal/ui/hittest.go` were built for the abandoned `WS_EX_TRANSPARENT` design and now have no caller. Delete both, and delete `TestHit` from `internal/ui/hittest_test.go`. Keep `Rect`, `rectRegistry`, `newRectRegistry`, and both registry tests — the region path uses all of them.

Update the doc comment on `newRectRegistry` so it describes the region fallback rather than click-through:

```go
// newRectRegistry builds a registry for a canvas cssWidth CSS pixels wide.
//
// The fallback matters more here than it looks: the window is clipped to the
// region built from these rects, so an empty registry would clip the UI away
// entirely. It covers the island's largest presence — sheet, 720x520 —
// centered and top-anchored, so a JS failure leaves a usable window rather
// than an invisible one.
```

Run the remaining tests:

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -v
```

Expected: PASS — `TestAssetServer*` (3), `TestRectRegistry*` (2), `TestRegion*` (3). `TestHit` is gone.

- [ ] **Step 6: Write `canvas.go`**

```go
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
	x := (sw - pw) / 2

	win.SetWindowPos(c.hwnd, win.HWND_TOPMOST, x, 0, pw, ph,
		win.SWP_NOACTIVATE|win.SWP_FRAMECHANGED)

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
```

- [ ] **Step 7: Rewire `overlay.go` startup**

Replace the `go func(){ time.Sleep(250ms) ... }()` block (currently `:367-381`) with a canvas held in a package var, attached from a new `uiReady` binding:

```go
var canvasGlobal *canvas

// ...inside StartOverlay, alongside the other bindings:
	canvasGlobal = newCanvas(w)
	w.Bind("uiReady", func() {
		w.Dispatch(func() { canvasGlobal.Attach() })
	})
	w.Bind("getCanvasSize", func() map[string]float64 {
		return map[string]float64{"w": canvasCSSWidth, "h": canvasCSSHeight}
	})
	w.Bind("setRegionRects", func(rects []Rect) {
		w.Dispatch(func() { canvasGlobal.SetRects(rects) })
	})
```

Delete the `callResize` binding entirely. Delete `resizeWindow`, `defaultW`, `defaultH`, `procRedrawWindow`, and `procGetWindowRect`. **Keep** `dpiScale`, `procGetDpiForWindow`, `procCreateRoundRectRgn`, and `procSetWindowRgn`.

- [ ] **Step 8: Convert the page to fixed-window layout**

In `internal/ui/assets/index.html`, replace the `html,body` rule:

```css
html,body{width:100%;height:100%;overflow:hidden;background:transparent;color:var(--ink);
  font-family:var(--font);-webkit-font-smoothing:antialiased;letter-spacing:-0.01em;cursor:default}
#shell{position:fixed;inset:0}
```

Every surface currently uses `position:absolute;inset:0` and relied on the *window* being its size. Anchor them explicitly instead:

```css
.pill{position:fixed;top:10px;left:50%;transform:translateX(-50%);
  width:300px;height:52px;display:flex;align-items:center;gap:11px;
  border-radius:26px;padding:0 8px 0 18px;cursor:pointer;overflow:hidden}
.panel{display:none;position:fixed;top:10px;left:50%;transform:translateX(-50%);
  z-index:4;flex-direction:column;overflow:hidden;border-radius:26px}
.panel.active{display:flex}
#commandPanel{width:720px;height:440px}
#outputPanel{width:720px;height:540px}
#confirmPanel{width:680px;height:410px}
.card{position:fixed;top:10px;left:50%;transform:translateX(-50%);
  width:400px;height:150px;z-index:4;border-radius:22px;padding:17px 19px;
  text-align:left;display:none;flex-direction:column;justify-content:center}
.card.shown{display:flex}
#dashboard{position:fixed;top:20px;left:50%;transform:translateX(-50%);
  width:1160px;height:760px;z-index:5;display:none;border-radius:26px;overflow:hidden}
#dashboard.visible{display:grid;grid-template-columns:248px 1fr}
```

Replace `sizeTo()` — it no longer resizes anything, it reports the shape:

```js
/* Publish every visible surface to Go, which unions them into the window
   region. Anything not covered by these rects is not part of the window at all,
   so clicks there land on the desktop. Called on visibility changes, and on
   morph start/settle from Task 6 — never per animation frame. */
function publishRegionRects(){
  const rects=[];
  document.querySelectorAll('.pill, .panel.active, .card.shown, #dashboard.visible').forEach(el=>{
    const cs=getComputedStyle(el);
    if(cs.display==='none') return;
    const r=el.getBoundingClientRect();
    if(r.width>0 && r.height>0) rects.push({x:r.left,y:r.top,w:r.width,h:r.height});
  });
  jlog('regionRects '+rects.length);
  window.setRegionRects && window.setRegionRects(rects);
}
function sizeTo(_v){ publishRegionRects(); }
```

At the end of the script, replace the boot line with:

```js
jlog('overlay loaded');loadSettings();updateUI('idle','Ready');
window.uiReady && window.uiReady();
requestAnimationFrame(publishRegionRects);
```

Also call `publishRegionRects()` at the end of `refreshPillVisibility()`, `setPanel()`, `openSettings()`, `closeSettings()`, and `hideSuggestion()` so no visibility change leaves the region stale.

- [ ] **Step 9: Build and verify by hand**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

Verify all of:

1. The pill appears top-center and is clickable.
2. **Click on the desktop beside and below the pill — it must reach the desktop.** Open a browser and click a tab in the row beside the pill. This is the check the whole redesign exists for.
3. The pill is a clean capsule with no square transparent halo around it (region radius correct).
4. The gear opens the Control Center; it is fully visible and not clipped at any edge.
5. Esc closes it and the region returns to the pill shape — no leftover invisible clickable box.
6. `voice-agent.log` shows `[ui/canvas] fixed 1200x800` with a sane physical size, and `regionRects N` lines on state changes only — **not** continuously.

- [ ] **Step 10: Delete the dead spike**

```bash
rm -rf cmd/spike-clickthrough
```

- [ ] **Step 11: Commit**

```bash
git add internal/ui/canvas.go internal/ui/region.go internal/ui/region_test.go \
        internal/ui/hittest.go internal/ui/hittest_test.go \
        internal/ui/overlay.go internal/ui/assets/index.html
git rm -r --cached cmd/spike-clickthrough 2>/dev/null || true
git commit -m "feat(ui): fixed-size window shaped by SetWindowRgn

Window is created once at 1200x800 and never resized; its visible shape is a
region unioned from the rects JS publishes, so clicks outside it reach the
desktop without any WS_EX_TRANSPARENT flag (that spike failed). Deletes
resizeWindow, the VIEW size map, callResize, the RedrawWindow ghost-buster,
and hit()/Point from the abandoned click-through design.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

### Task 4: Typed Go→JS bridge

Replace string-interpolated `w.Eval` with one JSON envelope. This closes the `[object Object]` bug class (`d4e5cdd`) structurally rather than by remembering to quote, and adds ready-gating so early pushes aren't dropped.

**Files:**
- Create: `internal/ui/bridge.go`
- Create: `internal/ui/bridge_test.go`
- Modify: `internal/ui/overlay.go` — all `w.Eval` call sites; `internal/ui/suggestion.go`
- Modify: `internal/ui/assets/index.html` — add `window.__agent.recv`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `func envelope(kind string, payload any) (string, error)` — pure; returns the JS snippet
  - `type Bridge struct{...}`, `func newBridge(w webview.WebView) *Bridge`
  - `func (b *Bridge) Push(kind string, payload any)`
  - `func (b *Bridge) Ready()` — flushes buffered pushes in order

- [ ] **Step 1: Write the failing test**

```go
// internal/ui/bridge_test.go
package ui

import (
	"encoding/json"
	"strings"
	"testing"
)

// The envelope must survive payloads that used to break string interpolation.
// Regression for d4e5cdd ("approval card showed [object Object]").
func TestEnvelopeHostilePayloads(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		payload any
	}{
		{"double quotes", "notify", map[string]string{"text": `he said "hi"`}},
		{"single quotes", "notify", map[string]string{"text": `it's fine`}},
		{"newlines", "notify", map[string]string{"text": "line1\nline2\r\nline3"}},
		{"script close tag", "notify", map[string]string{"text": "</script><script>evil()</script>"}},
		{"backslash", "notify", map[string]string{"text": `C:\Users\Eshwar\file.txt`}},
		{"emoji", "notify", map[string]string{"text": "done ✅ 🎉"}},
		{"invalid utf8", "notify", map[string]string{"text": "bad\xff\xfebytes"}},
		{"nested object", "activity:update", map[string]any{
			"id": "trust.approval", "data": map[string]any{"steps": []string{"a", "b"}}}},
		{"nil payload", "surface:close", nil},
	}

	for _, tc := range cases {
		js, err := envelope(tc.kind, tc.payload)
		if err != nil {
			t.Errorf("%s: envelope returned error: %v", tc.name, err)
			continue
		}
		// Nothing that could terminate the surrounding script context may survive.
		if strings.Contains(js, "</script") {
			t.Errorf("%s: raw </script survived into JS: %s", tc.name, js)
		}
		if strings.ContainsAny(js, "\n\r") {
			t.Errorf("%s: raw newline survived into JS: %q", tc.name, js)
		}
		// The argument must be valid JSON that round-trips to the same kind.
		open := strings.Index(js, "(")
		closeIdx := strings.LastIndex(js, ")")
		if open < 0 || closeIdx < open {
			t.Fatalf("%s: cannot find call arguments in %q", tc.name, js)
		}
		var got struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(js[open+1:closeIdx]), &got); err != nil {
			t.Errorf("%s: argument is not valid JSON: %v (%s)", tc.name, err, js)
			continue
		}
		if got.Kind != tc.kind {
			t.Errorf("%s: kind = %q, want %q", tc.name, got.Kind, tc.kind)
		}
	}
}

func TestEnvelopeCallsRecv(t *testing.T) {
	js, err := envelope("state", map[string]string{"state": "listening"})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if !strings.Contains(js, "__agent") || !strings.Contains(js, "recv") {
		t.Errorf("envelope does not call __agent.recv: %s", js)
	}
}

func TestBridgeBuffersUntilReady(t *testing.T) {
	var sent []string
	b := &Bridge{eval: func(js string) { sent = append(sent, js) }}

	b.Push("state", map[string]string{"state": "listening"})
	b.Push("state", map[string]string{"state": "thinking"})
	if len(sent) != 0 {
		t.Fatalf("pushed %d evals before Ready, want 0", len(sent))
	}

	b.Ready()
	if len(sent) != 2 {
		t.Fatalf("flushed %d evals, want 2", len(sent))
	}
	if !strings.Contains(sent[0], "listening") || !strings.Contains(sent[1], "thinking") {
		t.Errorf("flush lost ordering: %v", sent)
	}

	b.Push("state", map[string]string{"state": "idle"})
	if len(sent) != 3 {
		t.Errorf("post-Ready push was buffered instead of sent immediately")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -run "TestEnvelope|TestBridge" -v
```

Expected: FAIL — `undefined: envelope`, `undefined: Bridge`

- [ ] **Step 3: Write the implementation**

```go
// internal/ui/bridge.go
package ui

import (
	"encoding/json"
	"log"
	"sync"

	webview "github.com/webview/webview_go"
)

// envelope renders one Go->JS message as a JavaScript call.
//
// The entire payload is marshalled as a single JSON value, so no caller-supplied
// string ever becomes JavaScript source. Go's json.Marshal escapes <, > and &
// to \u003c/\u003e/\u0026 by default, which is exactly what keeps a payload
// containing "</script>" from terminating the script context.
func envelope(kind string, payload any) (string, error) {
	env := struct {
		Kind string `json:"kind"`
		Data any    `json:"data,omitempty"`
	}{Kind: kind, Data: payload}

	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return "window.__agent&&window.__agent.recv(" + string(raw) + ")", nil
}

// Bridge is the single Go->JS channel. Pushes made before the page has loaded
// are buffered and flushed in order once JS calls uiReady — replacing the
// time.Sleep(250ms) race that used to guard startup.
type Bridge struct {
	mu    sync.Mutex
	ready bool
	buf   []string
	eval  func(js string) // injectable for tests
}

func newBridge(w webview.WebView) *Bridge {
	return &Bridge{eval: func(js string) { w.Dispatch(func() { w.Eval(js) }) }}
}

func (b *Bridge) Push(kind string, payload any) {
	js, err := envelope(kind, payload)
	if err != nil {
		log.Printf("[ui/bridge] marshal %s: %v", kind, err)
		return
	}
	b.mu.Lock()
	if !b.ready {
		b.buf = append(b.buf, js)
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	b.eval(js)
}

func (b *Bridge) Ready() {
	b.mu.Lock()
	b.ready = true
	pending := b.buf
	b.buf = nil
	b.mu.Unlock()
	for _, js := range pending {
		b.eval(js)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/ -run "TestEnvelope|TestBridge" -v
```

Expected: PASS (3 tests)

- [ ] **Step 5: Add the JS receiver**

At the top of the `<script>` block in `internal/ui/assets/index.html`:

```js
/* Single entry point for everything Go pushes. Handlers are registered by
   feature modules; unknown kinds are logged and dropped, never thrown, so a
   stale Go build can't take down the render loop. */
window.__agent = {
  handlers: {},
  on(kind, fn){ (this.handlers[kind] ||= []).push(fn) },
  recv(env){
    try{
      const hs = this.handlers[env.kind];
      if(!hs || !hs.length){ jlog('unhandled event '+env.kind); return }
      for(const h of hs) h(env.data);
    }catch(e){ jlog('recv error '+env.kind+': '+e) }
  }
};
window.__agent.on('state', d => updateUI(d.state, d.text));
window.__agent.on('notify', d => updateUI('idle', d.text));
window.__agent.on('surface:open', d => {
  if(d.id==='command') showCommand();
  else if(d.id==='result') showCard(d.text);
  else if(d.id==='approve') showConfirmCard(d.card);
});
window.__agent.on('activity:update', d => {
  if(d.id==='ambient.nudge')
    showSuggestion(d.data.id, d.data.icon, d.data.title, d.data.message, d.data.action);
});
```

- [ ] **Step 6: Convert every Go eval call site**

Add `var bridge *Bridge` as a package var; construct it in `StartOverlay` (`bridge = newBridge(w)`) and call `bridge.Ready()` inside the existing `uiReady` binding, before `canvasGlobal.Attach()`.

Rewrite these to use the bridge — no `fmt.Sprintf` into JS anywhere:

| Location | Was | Becomes |
|---|---|---|
| `overlay.go:98` `SetState` | `updateUI('%s')` | `bridge.Push("state", map[string]string{"state": sk})` |
| `overlay.go:131` `ShowNotification` | `updateUI('idle', %s)` | `bridge.Push("notify", map[string]string{"text": text})` |
| `overlay.go:137` notif timer | `updateUI('idle')` | `bridge.Push("notify", map[string]string{"text": ""})` |
| `overlay.go:149` `SetMeetingAlert` | `triggerMeetingAlert(...)` | `bridge.Push("activity:update", map[string]any{"id":"ambient.nudge","data":map[string]any{"id":"meeting","icon":"calendar","title":title,"message":text,"action":""}})` |
| `overlay.go:157` `ShowCommandBarInOverlay` | `showCommand()` | `bridge.Push("surface:open", map[string]string{"id":"command"})` |
| `overlay.go:169` `ShowOutputOverlay` | `showCard(%s)` | `bridge.Push("surface:open", map[string]any{"id":"result","text":text})` |
| `overlay.go:182` `RequestConfirmationCard` | `showConfirmCard(%s)` | `bridge.Push("surface:open", map[string]any{"id":"approve","card":cardJSON})` |
| `overlay.go:192` `RequestConfirmation` | `showConfirm(%s)` | `bridge.Push("surface:open", map[string]any{"id":"approve","card":msg})` |
| `suggestion.go:24` `ShowSuggestion` | `showSuggestion(...)` | `bridge.Push("activity:update", map[string]any{"id":"ambient.nudge","data":map[string]any{"id":id,"icon":icon,"title":title,"message":message,"action":action}})` |
| `overlay.go:396` etc. auth refresh | `loadIntegrationStatusesDash()` | leave as-is — it takes no arguments, so there is nothing to interpolate |

Delete the now-unused `encoding/json` import from `suggestion.go`, and the double-marshal comment block at `overlay.go:174-179` (it documented a problem that no longer exists).

- [ ] **Step 7: Build and verify by hand**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/... && go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

Type `ai say hello` in the command bar; confirm the result panel renders text (not `[object Object]`). Confirm `voice-agent.log` has no `unhandled event` lines during a normal run.

- [ ] **Step 8: Commit**

```bash
git add internal/ui/bridge.go internal/ui/bridge_test.go internal/ui/overlay.go internal/ui/suggestion.go internal/ui/assets/index.html
git commit -m "feat(ui): typed Go->JS bridge with ready-gating

One JSON envelope replaces string-interpolated w.Eval calls, closing the
[object Object] bug class structurally. Pushes before page load are
buffered and flushed on uiReady, removing the 250ms startup sleep.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: Island state resolution and motion engine

The island's two axes — presence (geometry) and content — plus the springs that move between them. This is where it starts to feel like a Dynamic Island.

**Files:**
- Create: `internal/ui/assets/js/state.js`
- Create: `internal/ui/assets/js/state.test.js`
- Create: `internal/ui/assets/js/motion.js`
- Create: `internal/ui/assets/css/island.css`
- Create: `internal/ui/assets/icons.svg`
- Modify: `internal/ui/assets/index.html`

**Interfaces:**
- Consumes: `window.__agent.on` (Task 4); `publishRegionRects` (Task 3)
- Produces:
  - `state.js`: `export const PRESENCE_SIZES`, `export function resolve(store)` → `{presence, contentId, surface}`, `export function topActivity(activities)`
  - `motion.js`: `export function morphTo(el, presence, opts)`, `export function swapContent(el, renderFn)`

- [ ] **Step 1: Write the failing test**

```js
// internal/ui/assets/js/state.test.js
import test from 'node:test';
import assert from 'node:assert/strict';
import { resolve, topActivity, PRESENCE_SIZES } from './state.js';

const base = {
  surface: null, activities: [], agentState: 'idle',
  hover: false, idleSince: 0, now: 0,
};
const s = (o) => ({ ...base, ...o });

test('idle collapses to dormant after 6s with cursor away', () => {
  assert.equal(resolve(s({ now: 5999 })).presence, 'compact');
  assert.equal(resolve(s({ now: 6001 })).presence, 'dormant');
});

test('hover always wins over dormant', () => {
  assert.equal(resolve(s({ now: 60000, hover: true })).presence, 'peek');
});

test('open surface outranks every activity', () => {
  const r = resolve(s({
    surface: 'command',
    activities: [{ id: 'trust.approval', priority: 100 }],
  }));
  assert.equal(r.presence, 'sheet');
  assert.equal(r.contentId, 'command');
});

test('control center leaves the island compact', () => {
  const r = resolve(s({ surface: 'controlcenter' }));
  assert.equal(r.presence, 'compact');
  assert.equal(r.surface, 'controlcenter');
});

test('approval auto-expands and outranks a running agent', () => {
  const r = resolve(s({
    agentState: 'acting',
    activities: [
      { id: 'agent.run', priority: 90 },
      { id: 'trust.approval', priority: 100 },
    ],
  }));
  assert.equal(r.presence, 'expanded');
  assert.equal(r.contentId, 'trust.approval');
});

test('now-playing loses to a running agent', () => {
  const r = resolve(s({
    activities: [
      { id: 'spotify.nowplaying', priority: 20 },
      { id: 'agent.run', priority: 90 },
    ],
  }));
  assert.equal(r.contentId, 'agent.run');
});

test('a running agent never sits dormant', () => {
  const r = resolve(s({ now: 999999, agentState: 'listening',
    activities: [{ id: 'agent.run', priority: 90 }] }));
  assert.equal(r.presence, 'compact');
});

test('topActivity picks highest priority, stable for ties', () => {
  assert.equal(topActivity([]), null);
  assert.equal(topActivity([{ id: 'a', priority: 1 }, { id: 'b', priority: 9 }]).id, 'b');
  assert.equal(topActivity([{ id: 'a', priority: 5 }, { id: 'b', priority: 5 }]).id, 'a');
});

test('every presence has a size', () => {
  for (const p of ['dormant', 'compact', 'peek', 'expanded', 'sheet']) {
    assert.ok(PRESENCE_SIZES[p], `${p} has no size`);
    assert.ok(PRESENCE_SIZES[p].w > 0 && PRESENCE_SIZES[p].h > 0);
  }
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --test internal/ui/assets/js/
```

Expected: FAIL — cannot find module `./state.js`

If `node` is not installed, skip Steps 2 and 4 and note it in the commit message. These tests are deliberately **not** wired into the Go build; Node is not a dependency of this project.

- [ ] **Step 3: Write `state.js`**

```js
// internal/ui/assets/js/state.js
// Pure island state resolution. No DOM, no globals — so it can be tested with
// `node --test` and reasoned about without running Windows.

export const PRESENCE_SIZES = {
  dormant:  { w: 168, h: 32,  r: 16, opacity: 0.5 },
  compact:  { w: 260, h: 40,  r: 20, opacity: 1 },
  peek:     { w: 420, h: 52,  r: 26, opacity: 1 },
  expanded: { w: 560, h: 180, r: 28, opacity: 1 },
  sheet:    { w: 720, h: 520, r: 30, opacity: 1 },
};

export const DORMANT_AFTER_MS = 6000;

// Highest priority wins; ties resolve to whichever registered first, so a
// steady stream of same-priority updates can't make the island flicker.
export function topActivity(activities) {
  if (!activities || !activities.length) return null;
  let best = activities[0];
  for (const a of activities) if (a.priority > best.priority) best = a;
  return best;
}

// resolve maps the whole store to exactly one {presence, contentId, surface}.
// This is the ONLY function allowed to decide the island's size. Everything
// else mutates the store and re-runs it, which is what makes a stray state
// update unable to snap the geometry.
export function resolve(store) {
  const { surface, agentState, hover, idleSince, now } = store;

  // 1. User intent outranks everything the agent wants to say.
  if (surface === 'controlcenter') {
    return { presence: 'compact', contentId: 'idle', surface: 'controlcenter' };
  }
  if (surface) {
    return { presence: 'sheet', contentId: surface, surface: null };
  }

  // 2. Approvals block a plan, so they auto-expand.
  const top = topActivity(store.activities);
  if (top && top.id === 'trust.approval') {
    return { presence: 'expanded', contentId: 'trust.approval', surface: null };
  }

  // 3. Any other live activity: peek on hover, otherwise compact.
  if (top) {
    return { presence: hover ? 'peek' : 'compact', contentId: top.id, surface: null };
  }

  // 4. A working agent is never allowed to go dormant.
  if (agentState && agentState !== 'idle') {
    return { presence: hover ? 'peek' : 'compact', contentId: 'agent.run', surface: null };
  }

  // 5. Truly idle — shrink out of the way.
  if (hover) return { presence: 'peek', contentId: 'idle', surface: null };
  const dormant = (now - idleSince) > DORMANT_AFTER_MS;
  return { presence: dormant ? 'dormant' : 'compact', contentId: 'idle', surface: null };
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
node --test internal/ui/assets/js/
```

Expected: PASS (8 tests)

- [ ] **Step 5: Write `motion.js`**

```js
// internal/ui/assets/js/motion.js
import { PRESENCE_SIZES } from './state.js';

const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// Grow overshoots ~4% and settles; shrink does not. A bouncing retreat reads as
// unstable, while a bouncing arrival reads as physical. Same reason iOS uses
// asymmetric curves here.
const GROW   = { dur: 460, ease: 'cubic-bezier(.22,1.16,.36,1)' };
const SHRINK = { dur: 380, ease: 'cubic-bezier(.36,0,.24,1)' };

export function morphTo(el, presence, onSettled) {
  const to = PRESENCE_SIZES[presence];
  if (!to) return;

  const growing = to.w * to.h > el.offsetWidth * el.offsetHeight;
  const t = reduced ? { dur: 0, ease: 'linear' } : (growing ? GROW : SHRINK);

  el.style.transition =
    `width ${t.dur}ms ${t.ease}, height ${t.dur}ms ${t.ease}, ` +
    `border-radius ${t.dur}ms ${t.ease}, opacity 200ms linear`;
  el.style.width = to.w + 'px';
  el.style.height = to.h + 'px';
  el.style.borderRadius = to.r + 'px';
  el.style.opacity = to.opacity;
  el.dataset.presence = presence;

  clearTimeout(el.__morphTimer);
  el.__morphTimer = setTimeout(() => onSettled && onSettled(), t.dur);
}

// Content lags the shape: the container reaches its new size BEFORE the new
// content lands. This single detail is what separates a morphing object from a
// box that resizes.
export function swapContent(host, render) {
  if (reduced) { host.innerHTML = ''; host.appendChild(render()); return }

  const outgoing = host.firstElementChild;
  if (outgoing) {
    outgoing.style.transition = 'opacity 120ms linear, transform 120ms linear, filter 120ms linear';
    outgoing.style.opacity = '0';
    outgoing.style.transform = 'scale(.96)';
    outgoing.style.filter = 'blur(4px)';
    setTimeout(() => outgoing.remove(), 120);
  }

  const incoming = render();
  incoming.style.opacity = '0';
  incoming.style.transform = 'scale(.96)';
  host.appendChild(incoming);
  setTimeout(() => {
    incoming.style.transition = 'opacity 200ms linear, transform 200ms linear';
    incoming.style.opacity = '1';
    incoming.style.transform = 'scale(1)';
  }, 90);
}
```

- [ ] **Step 6: Write `island.css` and the icon sprite**

```css
/* internal/ui/assets/css/island.css */
#island{
  position:fixed; top:10px; left:50%; margin-left:0;
  transform:translateX(-50%);
  width:260px; height:40px; border-radius:20px;
  display:flex; align-items:center; overflow:hidden;
  cursor:pointer; z-index:6;
  will-change:width,height,border-radius;
}
/* Caps translate, never cross-fade — that is what gives the island object
   permanence when its content changes underneath. */
#island .cap{
  flex:0 0 auto; width:28px; height:28px; margin:0 6px;
  display:grid; place-items:center; border-radius:50%;
  transition:transform 460ms cubic-bezier(.22,1.16,.36,1), opacity 200ms linear;
}
#island .cap.lead{margin-left:8px}
#island .cap.trail{margin-right:8px;margin-left:auto}
#island .body{
  flex:1 1 auto; min-width:0; position:relative; height:100%;
  display:flex; align-items:center;
}
#island .body > *{position:absolute; inset:0; display:flex; align-items:center; gap:8px}
#island[data-presence="expanded"] .body > *,
#island[data-presence="sheet"] .body > *{align-items:flex-start; flex-direction:column; padding:14px 4px}
#island .ttl{font-size:13px;font-weight:560;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
#island .sub{font-size:11.5px;color:var(--ink-2)}
.ico{width:20px;height:20px;stroke:currentColor;stroke-width:1.75;fill:none;
  stroke-linecap:round;stroke-linejoin:round}
@media (prefers-reduced-motion:reduce){
  #island,#island .cap{transition:none!important}
}
```

**ERRATUM (Task 5 review): do NOT create a separate `icons.svg` file.** Step 7 inlines a byte-identical copy of this sprite into `index.html`, and nothing ever reads the standalone file, so the two copies silently drift. The sprite must live inline in the document anyway for `<use href="#i-mic">` to resolve. Inline it in Step 7 only. The markup below is still the sprite to use. Each icon is a `<symbol>` with `viewBox="0 0 24 24"`; used as `<svg class="ico"><use href="#i-mic"/></svg>`.

```html
<svg xmlns="http://www.w3.org/2000/svg" style="display:none">
  <symbol id="i-mic" viewBox="0 0 24 24"><rect x="9" y="2" width="6" height="12" rx="3"/><path d="M5 11a7 7 0 0 0 14 0M12 18v4"/></symbol>
  <symbol id="i-wave" viewBox="0 0 24 24"><path d="M3 12h2M7 8v8M11 5v14M15 8v8M19 11h2"/></symbol>
  <symbol id="i-spotify" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M7.5 9.5c3-1 6.5-.6 9 1M8 13c2.4-.8 5-.5 7 .8M8.5 16c1.8-.6 3.8-.4 5.5.6"/></symbol>
  <symbol id="i-mail" viewBox="0 0 24 24"><rect x="3" y="5" width="18" height="14" rx="2"/><path d="m3 7 9 6 9-6"/></symbol>
  <symbol id="i-calendar" viewBox="0 0 24 24"><rect x="3" y="5" width="18" height="16" rx="2"/><path d="M8 3v4M16 3v4M3 10h18"/></symbol>
  <symbol id="i-download" viewBox="0 0 24 24"><path d="M12 3v12M7 11l5 5 5-5M4 20h16"/></symbol>
  <symbol id="i-link" viewBox="0 0 24 24"><path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1"/><path d="M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1"/></symbol>
  <symbol id="i-shield" viewBox="0 0 24 24"><path d="M12 3l7 3v6c0 5-3 8-7 9-4-1-7-4-7-9V6z"/></symbol>
  <symbol id="i-gear" viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M19.1 4.9L17 7M7 17l-2.1 2.1"/></symbol>
  <symbol id="i-stop" viewBox="0 0 24 24"><rect x="7" y="7" width="10" height="10" rx="2"/></symbol>
  <symbol id="i-chevron" viewBox="0 0 24 24"><path d="m9 6 6 6-6 6"/></symbol>
  <symbol id="i-sparkle" viewBox="0 0 24 24"><path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/></symbol>
  <symbol id="i-search" viewBox="0 0 24 24"><circle cx="11" cy="11" r="6"/><path d="m16 16 4 4"/></symbol>
  <symbol id="i-folder" viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></symbol>
  <symbol id="i-terminal" viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3M13 15h4"/></symbol>
</svg>
```

- [ ] **Step 7: Replace the pill with the island in `index.html`**

Replace the `<div class="pill glass" id="statusPill">` block with:

```html
  <div id="island" class="glass" onclick="handleIslandClick()">
    <span class="cap lead" id="capLead"></span>
    <span class="body" id="islandBody"></span>
    <span class="cap trail" id="capTrail"></span>
  </div>
```

Add to `<head>`: `<link rel="stylesheet" href="css/island.css"/>`, and change the script tag to `<script type="module" src="js/main.js"></script>`. Inline the icon sprite as the first child of `<body>` (fetch-free — it must be in the document, not linked, so `<use href="#i-mic">` resolves).

Move the existing inline script into `internal/ui/assets/js/main.js`, add `import` lines for `state.js`/`motion.js`, and expose the functions the bridge handlers call (`updateUI`, `showCommand`, `showCard`, `showConfirmCard`, `showSuggestion`, `publishRegionRects`) on `window` — ES modules do not create globals, and the `onclick=` attributes in the markup need them.

Wire the store and the render loop:

```js
// in main.js
import { resolve, PRESENCE_SIZES } from './state.js';
import { morphTo, swapContent } from './motion.js';

const store = { surface:null, activities:[], agentState:'idle',
                hover:false, idleSince:Date.now(), now:Date.now() };
let applied = { presence:null, contentId:null };

/* The region must never be smaller than what CSS is painting at any instant of
   a morph, or the island gets visibly clipped mid-animation. Rather than
   animating the region in lockstep (which needs per-frame IPC or the easing
   curve duplicated in Go), publish the BOUNDING BOX of the from- and to-shapes
   for the duration, then the exact shape once it settles. Two calls, not sixty.

   Cost: for the ~460ms of a grow, the surplus area is transparent window that
   eats clicks. Bounded, brief, and confined to where the island is about to be. */
function unionRegionRect(fromPresence, toPresence){
  const a = PRESENCE_SIZES[fromPresence], b = PRESENCE_SIZES[toPresence];
  if(!a || !b) return null;
  const r = island.getBoundingClientRect();
  const cx = r.left + r.width/2, top = r.top;
  const w = Math.max(a.w, b.w), h = Math.max(a.h, b.h);
  return {x: cx - w/2, y: top, w, h};
}

> **ERRATUM (found in Task 5 review, fixed in Task 5 fix round 1).** The
> `setRegionRects([u])` call below is WRONG as written. It *replaces* the whole
> published region with an island-only rect instead of unioning it with the other
> visible surfaces (`.panel.active`, `.card.shown`, `#dashboard.visible`), and when
> the island is `display:none` (Control Center open) `getBoundingClientRect()`
> returns zeros, producing a garbage rect pinned near the window origin that clips
> the open dashboard for 380–460ms. The widen phase must publish the SAME surface
> set as the settle phase, appending the widened island rect only when the island is
> actually visible. Corrected in the implementation; kept here with this note so the
> mistake is visible rather than silently rewritten.

export function rerender(){
  store.now = Date.now();
  const r = resolve(store);
  if(r.presence !== applied.presence){
    // Widen the region FIRST, so the growing island is never clipped.
    const u = unionRegionRect(applied.presence || r.presence, r.presence);
    if(u) window.setRegionRects && window.setRegionRects([u]);   // <-- see ERRATUM above
    // ...then narrow it to the exact shape once the morph settles.
    morphTo(island, r.presence, publishRegionRects);
    applied.presence = r.presence;
  }
  if(r.contentId !== applied.contentId){
    swapContent(islandBody, () => renderContentFor(r.contentId));
    applied.contentId = r.contentId;
  }
  setSurface(r.surface);
  publishRegionRects();
}

// Stub in this task; Task 7 replaces it with real surface routing. Defined now
// so rerender() does not throw before surfaces are modularized.
function setSurface(id){
  const dash = document.getElementById('dashboard');
  if(dash) dash.classList.toggle('visible', id === 'controlcenter');
}

// Trailing control on agent.run — hides the progress display without touching
// the running plan (see activities.js note in Task 6).
window.dismissRunDisplay = () => { endActivity('agent.run', syncAndRerender) };

island.addEventListener('mouseenter', () => { store.hover = true;  rerender() });
island.addEventListener('mouseleave', () => { store.hover = false; rerender() });
setInterval(() => { if(store.agentState==='idle' && !store.activities.length) rerender() }, 1000);
```

`renderContentFor(id)` returns a `<div>`; for now implement `'idle'` (label "Ready" + gear cap) and `'agent.run'` (label + equalizer). The remaining ids arrive in Task 6.

- [ ] **Step 8: Build and verify by hand**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

Verify: island sits top-center at 260×40; shrinks to 168×32 at 50% opacity after ~6s; grows to 420×52 on hover with a visible spring overshoot and settles without bouncing on the way back; content cross-fades rather than snapping; no console errors in `voice-agent.log`.

- [ ] **Step 9: Commit**

```bash
git add internal/ui/assets/
git commit -m "feat(ui): island state resolution, motion engine, icon sprite

Two orthogonal axes (presence/content) resolved by a single pure function;
asymmetric spring curves for grow vs shrink; content lags shape by 90ms;
caps translate rather than cross-fade. Adds a 15-icon inline SVG sprite.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: Live activity registry and the four v1 activities

**Files:**
- Create: `internal/ui/assets/js/activities.js`
- Create: `internal/ui/activity.go`
- Modify: `internal/ui/assets/js/main.js`, `internal/ui/overlay.go`, `internal/ui/suggestion.go`

**Interfaces:**
- Consumes: `resolve`, `topActivity` (Task 5); `Bridge.Push` (Task 4)
- Produces:
  - JS: `export function registerActivity(def)`, `export function updateActivity(id, data)`, `export function endActivity(id)`, `export function activeActivities()`
  - Go: `func UpdateActivity(id string, data any)`, `func EndActivity(id string)`

- [ ] **Step 1: Write the registry**

```js
// internal/ui/assets/js/activities.js
// A live activity owns the island's caps and body while it is the highest
// priority thing happening. Widgets in SP6 register through the same shape,
// with a placement instead of a priority.

const defs = new Map();   // id -> definition
const live = new Map();   // id -> { data, since, timer }

export function registerActivity(def) {
  if (!def || !def.id) return;
  defs.set(def.id, {
    priority: 0, leading: null, trailing: null,
    compact: null, expanded: null, ttl: 0, onDismiss: null, ...def,
  });
}

export function updateActivity(id, data, onChange) {
  const def = defs.get(id);
  if (!def) { window.jslog && window.jslog('[js] unknown activity ' + id); return }
  const prev = live.get(id);
  if (prev && prev.timer) clearTimeout(prev.timer);
  const entry = { data, since: prev ? prev.since : Date.now(), timer: 0 };
  // trust.approval deliberately has no ttl: auto-denying a plan the user is
  // still reading is worse than waiting forever.
  if (def.ttl > 0) entry.timer = setTimeout(() => endActivity(id, onChange), def.ttl);
  live.set(id, entry);
  onChange && onChange();
}

export function endActivity(id, onChange) {
  const e = live.get(id);
  if (e && e.timer) clearTimeout(e.timer);
  live.delete(id);
  onChange && onChange();
}

// Shape consumed by state.js resolve(): [{id, priority}]
export function activeActivities() {
  const out = [];
  for (const [id] of live) {
    const def = defs.get(id);
    if (def) out.push({ id, priority: def.priority });
  }
  return out;
}

export function renderActivity(id, slot) {
  const def = defs.get(id), e = live.get(id);
  if (!def || !e || !def[slot]) return null;
  return def[slot](e.data);
}
```

- [ ] **Step 2: Register the four activities**

Append to `activities.js`. `el(tag, cls, html)` is a tiny helper; define it at the top of the file.

```js
// esc lives here, not on window: activities.js is an ES module and must not
// depend on main.js having assigned a global before it loads.
export function esc(t) {
  return String(t == null ? '' : t)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}
function el(tag, cls, html) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (html != null) n.innerHTML = html;
  return n;
}
const icon = (name) => `<svg class="ico"><use href="#i-${name}"/></svg>`;

registerActivity({
  id: 'trust.approval', priority: 100, ttl: 0,
  leading: (d) => el('span', 'risk-' + (d.risk || 'risky'), icon('shield')),
  trailing: () => el('span'),
  compact: (d) => el('div', null, `<span class="ttl">${esc(d.title || 'Approve action?')}</span>`),
  expanded: (d) => {
    const n = el('div', null,
      `<span class="ttl">${esc(d.title || 'Approve action?')}</span>` +
      `<span class="sub">${esc(d.goal || '')}</span>`);
    const row = el('div', 'actions right');
    const no = el('button', 'btn ghost', 'Cancel');
    const yes = el('button', 'btn primary', 'Approve');
    no.onclick = () => window.resolveConfirm(false);
    yes.onclick = () => window.resolveConfirm(true);
    row.append(no, yes); n.appendChild(row);
    return n;
  },
});

registerActivity({
  id: 'agent.run', priority: 90, ttl: 0,
  leading: (d) => el('span', d.phase === 'listening' ? 'orb on pulse' : 'orb on warm', icon(
    d.phase === 'listening' ? 'mic' : 'sparkle')),
  trailing: (d) => {
    if (d.phase === 'listening') return el('span', 'eq', '<i></i><i></i><i></i><i></i><i></i>');
    // NOTE: this dismisses the island's progress display; it does NOT abort the
    // running plan. Real cancellation needs an engine-side binding, and this
    // spec is constrained to internal/ui only. Deferred to SP6 — do not fake it
    // by hiding the UI and letting the plan keep running silently, so the glyph
    // is a chevron (collapse), not a stop square.
    const b = el('button', 'iconbtn', icon('chevron'));
    b.title = 'Hide progress';
    b.onclick = (ev) => { ev.stopPropagation(); window.dismissRunDisplay() };
    return b;
  },
  compact: (d) => el('div', null, `<span class="ttl">${esc(d.text || 'Working…')}</span>`),
  expanded: (d) => el('div', null,
    `<span class="ttl">${esc(d.text || 'Working…')}</span>` +
    `<span class="sub">${esc(d.detail || '')}</span>`),
});

registerActivity({
  id: 'ambient.nudge', priority: 50, ttl: 8000,
  leading: (d) => el('span', null, icon(({ download: 'download', calendar: 'calendar',
    link: 'link', warn: 'shield' })[d.icon] || 'sparkle')),
  trailing: (d) => {
    if (!d.action) return el('span');
    const b = el('button', 'btn primary', esc(d.action));
    // acceptSuggestion currently reads el.dataset.id and takes no argument
    // (index.html:305). It gains an explicit id parameter in this task:
    //   window.acceptSuggestion = (id) => { window.suggestionAccept &&
    //     window.suggestionAccept(id); endActivity('ambient.nudge', syncAndRerender) }
    b.onclick = (ev) => { ev.stopPropagation(); window.acceptSuggestion(d.id) };
    return b;
  },
  compact: (d) => el('div', null, `<span class="ttl">${esc(d.title || '')}</span>`),
  expanded: (d) => el('div', null,
    `<span class="ttl">${esc(d.title || '')}</span><span class="sub">${esc(d.message || '')}</span>`),
});

registerActivity({
  id: 'spotify.nowplaying', priority: 20, ttl: 0,
  leading: (d) => d.art
    ? el('span', 'art', `<img src="${esc(d.art)}" alt="" width="28" height="28" style="border-radius:6px"/>`)
    : el('span', null, icon('spotify')),
  trailing: () => el('span', 'eq', '<i></i><i></i><i></i><i></i><i></i>'),
  compact: (d) => el('div', null,
    `<span class="ttl">${esc(d.track || '')} <span class="sub">· ${esc(d.artist || '')}</span></span>`),
  expanded: (d) => el('div', null,
    `<span class="ttl">${esc(d.track || '')}</span><span class="sub">${esc(d.artist || '')}</span>`),
});
```

- [ ] **Step 3: Wire the registry into the render loop**

In `main.js`, replace `renderContentFor` and hook the bridge:

```js
import { registerActivity, updateActivity, endActivity, activeActivities, renderActivity }
  from './activities.js';

function slotFor(presence){
  return presence === 'expanded' || presence === 'sheet' ? 'expanded' : 'compact';
}
function renderContentFor(id, presence){
  if(id === 'idle'){
    const n = document.createElement('div');
    n.innerHTML = '<span class="ttl">Ready</span>';
    return n;
  }
  return renderActivity(id, slotFor(presence)) || document.createElement('div');
}
function renderCaps(id){
  capLead.replaceChildren(renderActivity(id,'leading')  || document.createTextNode(''));
  capTrail.replaceChildren(renderActivity(id,'trailing') || document.createTextNode(''));
}

window.__agent.on('activity:update', d => {
  updateActivity(d.id, d.data, syncAndRerender);
});
window.__agent.on('activity:end', d => endActivity(d.id, syncAndRerender));

function syncAndRerender(){ store.activities = activeActivities(); rerender() }
```

Update `rerender()` to call `renderCaps(r.contentId)` when `contentId` changes, and to pass `r.presence` into `renderContentFor`.

Map `state` events onto `agent.run` so all 30 existing Go call sites feed it:

```js
window.__agent.on('state', d => {
  store.agentState = d.state;
  if(d.state === 'idle'){ endActivity('agent.run', syncAndRerender); store.idleSince = Date.now() }
  else updateActivity('agent.run', { phase: d.state, text: d.text || defaultLabel(d.state) },
                      syncAndRerender);
});
window.__agent.on('notify', d => {
  // ShowNotification is the narration channel: orchestrator.go sends
  // "Step 2/5: …", research_tool.go sends "Reading: …". Feed the ticker.
  if(!d.text){ return }
  updateActivity('agent.run', { phase: store.agentState, text: d.text }, syncAndRerender);
});
```

- [ ] **Step 4: Add the Go-side activity API**

```go
// internal/ui/activity.go
package ui

// UpdateActivity pushes or refreshes a live activity in the island.
// Unknown ids are dropped by the JS registry with a log line, never thrown.
func UpdateActivity(id string, data any) {
	if bridge == nil {
		return
	}
	bridge.Push("activity:update", map[string]any{"id": id, "data": data})
}

// EndActivity removes a live activity.
func EndActivity(id string) {
	if bridge == nil {
		return
	}
	bridge.Push("activity:end", map[string]string{"id": id})
}
```

Rewrite `ShowSuggestion` in `suggestion.go` to call `UpdateActivity("ambient.nudge", ...)`, and `SetMeetingAlert` in `overlay.go` likewise, replacing the inline `bridge.Push` calls added in Task 4.

- [ ] **Step 5: Build and verify by hand**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/... && node --test internal/ui/assets/js/
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

Verify: run a multi-step command (`ai give me my Google Workspace brief`) and watch the island show the step ticker rather than `Working…`; trigger an action requiring approval and confirm the island expands inline with Approve/Cancel rather than taking over a full panel; confirm Cancel resolves the executor (the plan aborts, no hang).

- [ ] **Step 6: Commit**

```bash
git add internal/ui/activity.go internal/ui/suggestion.go internal/ui/overlay.go internal/ui/assets/
git commit -m "feat(ui): live activity registry + four v1 activities

Priority-sorted registry owning the island caps and body: trust.approval
(100, no ttl), agent.run (90), ambient.nudge (50, 8s ttl),
spotify.nowplaying (20). ShowNotification's 30 narration call sites now
drive the step ticker unchanged.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: Rehome surfaces and delete the old overlay

**Files:**
- Create: `internal/ui/assets/js/surfaces/{command,result,approve,controlcenter}.js`
- Create: `internal/ui/assets/css/{surfaces,controlcenter}.css`
- Modify: `internal/ui/assets/index.html`, `internal/ui/assets/js/main.js`
- Delete: `internal/ui/overlay.html`, `internal/ui/overlay_v2.html`

**Interfaces:**
- Consumes: everything from Tasks 3–6
- Produces: `export function openSurface(id, payload)`, `export function closeSurface()`

- [ ] **Step 1: Split the CSS**

Move the panel/card/button rules from the inline `<style>` into `css/surfaces.css`, and the `#dashboard`/`.sidebar`/`.nav`/`.metric`/`.surface`/`.field`/`.toggle` rules into `css/controlcenter.css`. Move `:root` tokens, `.glass`, and base resets into `css/tokens.css`. `index.html` keeps no inline `<style>` block.

- [ ] **Step 2: Split the JS by surface**

Each surface module exports `render(payload)` returning a DOM node plus an `id`. Move the corresponding functions out of `main.js` verbatim:

- `command.js` — `commandInput` markup, `fillSuggestion`, `submitCurrentCommand`, the keydown handler
- `result.js` — `renderContent`, `renderText`, `copyOutput`
- `approve.js` — `showConfirmCard` parsing (the `p.plan.steps` / `p.fields` logic), `resolveConfirm`
- `controlcenter.js` — `loadSettings`, `persistSettings`, `toggleFlag`, `setConn`, `loadIntegrationStatusesDash`, `switchTab`, `openSettings`, `closeSettings`

`main.js` keeps only the store, `rerender`, hover wiring, bridge handlers, `publishRegionRects`, and `openSurface`/`closeSurface`.

- [ ] **Step 3: Route surfaces through the store**

`openSurface(id, payload)` sets `store.surface = id`, stashes the payload, calls `setInputActive(true)` for `command`/`controlcenter`, and calls `rerender()`. `closeSurface()` clears both, calls `setInputActive(false)`, sets `store.idleSince = Date.now()`, and re-renders. No surface function may call `morphTo` or set a size directly — `resolve()` owns geometry.

Keep `Esc` bound to `closeSurface()` for every surface.

- [ ] **Step 4: Delete the dead files**

```bash
git rm internal/ui/overlay.html internal/ui/overlay_v2.html
```

`overlay.html` (1615 lines) has been unreferenced since the `overlay_v2` switch; `overlay_v2.html` was superseded by `assets/index.html` in Task 1.

- [ ] **Step 5: Build and run the full regression pass**

```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/ui/... && node --test internal/ui/assets/js/
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
./voice-agent.exe
```

Exercise every surface: command bar (type + Enter + history via Alt+Up), result panel (long output + Copy), approval (Approve and Cancel), Control Center (all four tabs, Save, an OAuth connect round-trip), suggestion nudge, and Quit.

- [ ] **Step 6: Commit**

```bash
git add internal/ui/assets/
git commit -m "refactor(ui): split surfaces into modules, delete legacy overlays

Command, result, approve, and Control Center become ES modules with their
own CSS; main.js keeps only the store and render loop. Surfaces no longer
set geometry — resolve() owns it. Removes overlay.html (1615 lines, dead)
and overlay_v2.html (superseded by assets/index.html).

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 8: Manual QA pass

Motion quality and click-through cannot be asserted in a unit test. This is the gate before the widget spec begins.

**Files:**
- Create: `docs/superpowers/plans/2026-07-29-sp5-qa-checklist.md`

- [ ] **Step 1: Run every check and record the result**

Write the checklist with an outcome column filled in for each row:

| # | Check | Pass criteria |
|---|---|---|
| 1 | Click-through | Click a browser tab directly beside the island's row — the tab activates, the overlay does not |
| 2 | Click-through, dormant | Same with the island dormant; also verify the dormant island itself is still clickable |
| 2a | No square halo | The island reads as a clean capsule — no rectangular dead zone around it swallowing clicks (region radius correct) |
| 2b | Region releases | Open the Control Center, close it, then click where it was — the click reaches the desktop, no invisible box left behind |
| 2c | Morph surplus | During a grow the surplus region eats clicks for ~460ms by design. Confirm it is not noticeable in normal use; if it is, reduce it by publishing the union later in the transition |
| 2d | Shadow clipping (design decision) | Judge whether the region-clipped island looks too flat. If so, apply spec §1's fallback: inflate the region by the shadow radius (~20px) and accept a click-eating halo. Record the decision |
| 3 | Hover latency | Cursor onto the island — peek begins with no perceptible delay |
| 4 | Grow curve | Idle → peek overshoots slightly and settles; no visible step or clipping |
| 5 | Shrink curve | Peek → compact does **not** bounce |
| 6 | Content lag | On content change the shape moves first; text fades in after, never reflows mid-morph |
| 7 | Cap permanence | With Spotify playing, hover: album art **slides**, does not blink out and back |
| 8 | DPI 100 / 125 / 150 | Change scale, restart, verify island geometry AND that the region still aligns with the painted edges at each (this is where the 2px inflation earns its keep) |
| 9 | Reduced motion | Enable Windows "Show animations off"; all transitions become instant cuts, no broken layout |
| 10 | Approval fail-closed | Trigger an approval, dismiss the island without answering → executor receives deny, no hang |
| 11 | Approval no-TTL | Trigger an approval, wait 60s → still waiting, has not auto-denied |
| 12 | No focus theft | Type continuously in Notepad while the agent runs a plan — no keystroke is lost |
| 13 | Step ticker | Multi-step command shows `N of M · <step>`, not `Working…` |
| 14 | Startup race | Launch 10× — the island always appears; no blank canvas, no dropped first event |
| 15 | Log hygiene | `voice-agent.log` has no `unhandled event`, no `unknown activity`, no JS errors |

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/plans/2026-07-29-sp5-qa-checklist.md
git commit -m "docs(sp5): manual QA checklist results

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Deferred to the widget platform spec (SP6)

Widget host and contract; widget placement/persistence; Spotify, web-search, and email widgets; multi-monitor; exclusive-fullscreen behavior; real DWM blur via per-frame `SetWindowRgn`; island drag/dock; a user-facing reduced-motion toggle.
