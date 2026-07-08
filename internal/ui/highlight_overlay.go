package ui

import (
	"sync"
	"syscall"
	"time"

	webview "github.com/webview/webview_go"
)

// ─────────────────────────────────────────────────────────────────────────────
// Highlight Overlay — flashes a red bounding box onto the screen before clicking.
// Kept alive in the background for zero-latency flashing.
// ─────────────────────────────────────────────────────────────────────────────

var (
	hlView      webview.WebView
	hlHWND      uintptr
	hlMutex     sync.Mutex
	hlVisible   bool
	hlHideTimer *time.Timer
	hlOnce      sync.Once
)

// ensureHighlightOverlay lazily starts the highlight overlay WebView on
// first use, replacing the old eager startup goroutine in main.go.
func ensureHighlightOverlay() {
	hlOnce.Do(func() { go RunHighlightOverlay() })
}

const hlHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8"/>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body {
    background: transparent;
    width: 100%; height: 100%;
    overflow: hidden;
  }
  .box {
    width: 100%; height: 100%;
    border: 4px solid #ef4444; /* red-500 */
    border-radius: 6px;
    box-shadow: 0 0 15px rgba(239, 68, 68, 0.5), inset 0 0 15px rgba(239, 68, 68, 0.3);
    background: rgba(239, 68, 68, 0.1);
    opacity: 0;
    transition: opacity 100ms ease;
  }
  .box.visible { opacity: 1; }
</style>
</head>
<body>
<div class="box" id="box"></div>
<script>
  function flash() {
    const box = document.getElementById('box');
    box.classList.add('visible');
  }
  function hide() {
    document.getElementById('box').classList.remove('visible');
  }
</script>
</body>
</html>`

// RunHighlightOverlay initializes the hidden highlight window. Run via goroutine in main.go.
func RunHighlightOverlay() {
	hlView = webview.New(false)
	hlView.SetTitle("")
	hlView.SetSize(100, 100, webview.HintFixed)
	hlView.SetHtml(hlHTML)

	// Apply Win32 layer transparency styles
	go func() {
		time.Sleep(300 * time.Millisecond)
		hlView.Dispatch(func() {
			applyHighlightOverlayStyle()
		})
	}()

	hlView.Run()
}

func applyHighlightOverlayStyle() {
	hwnd := uintptr(hlView.Window())
	if hwnd == 0 {
		return
	}
	hlHWND = hwnd

	moduser32dll := syscall.NewLazyDLL("user32.dll")
	getWL := moduser32dll.NewProc("GetWindowLongPtrW")
	setWL := moduser32dll.NewProc("SetWindowLongPtrW")
	setWP := moduser32dll.NewProc("SetWindowPos")

	style, _, _ := getWL.Call(hwnd, uintptr(uint32(0xFFFFFFF0)))
	style = style &^ (0x00C00000 | 0x00040000 | 0x00020000 | 0x00010000 | 0x00080000)
	style |= 0x80000000 // WS_POPUP
	setWL.Call(hwnd, uintptr(uint32(0xFFFFFFF0)), style)

	// WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_LAYERED | WS_EX_TRANSPARENT
	// WS_EX_TRANSPARENT (0x00000020) makes it click-through!
	exStyle, _, _ := getWL.Call(hwnd, uintptr(uint32(0xFFFFFFEC)))
	exStyle |= 0x00000008 | 0x00000080 | 0x00080000 | 0x00000020
	setWL.Call(hwnd, uintptr(uint32(0xFFFFFFEC)), exStyle)

	// Hide off-screen initially
	setWP.Call(hwnd, ^uintptr(0), 0, uintptr(0xFFFF), 10, 10, 0x0020|0x0040)
}

// FlashHighlightBox moves the window to (x,y,w,h), makes it visible, flashes, and hides it after `durationMs`.
func FlashHighlightBox(x, y, w, h int, durationMs int) {
	ensureHighlightOverlay()
	if hlView == nil || hlHWND == 0 {
		return
	}

	hlMutex.Lock()
	defer hlMutex.Unlock()

	if hlHideTimer != nil {
		hlHideTimer.Stop()
	}

	// Add padding to wrap AROUND the target, not obscuring entirely
	pad := 8
	px := x - pad
	py := y - pad
	pw := w + (pad * 2)
	ph := h + (pad * 2)

	hlView.Dispatch(func() {
		// Move and resize
		moduser32dll := syscall.NewLazyDLL("user32.dll")
		setWP := moduser32dll.NewProc("SetWindowPos")

		// Map it to HWND_TOPMOST (-1)
		setWP.Call(hlHWND, ^uintptr(0), uintptr(px), uintptr(py), uintptr(pw), uintptr(ph), 0x0040)
		hlView.Eval("flash();")
		hlVisible = true
	})

	// Auto-hide after duration if not explicitly hidden
	hlHideTimer = time.AfterFunc(time.Duration(durationMs)*time.Millisecond, func() {
		HideHighlightBox()
	})
}

// HideHighlightBox instantly hides the highlight box and moves it off-screen
// to ensure it doesn't intercept any subsequent mouse clicks.
func HideHighlightBox() {
	if hlView == nil || hlHWND == 0 {
		return
	}

	hlMutex.Lock()
	defer hlMutex.Unlock()

	hlView.Dispatch(func() {
		hlView.Eval("hide();")
		hlVisible = false

		// Move offscreen immediately to prevent click interception
		moduser32dll := syscall.NewLazyDLL("user32.dll")
		setWP := moduser32dll.NewProc("SetWindowPos")
		setWP.Call(hlHWND, ^uintptr(0), 0, uintptr(0xFFFF), 10, 10, 0x0040)
	})
}
