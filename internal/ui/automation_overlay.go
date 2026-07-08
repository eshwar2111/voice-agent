package ui

import (
	"encoding/json"
	"fmt"
	"sync"
	"syscall"
	"time"

	webview "github.com/webview/webview_go"
)

// ─────────────────────────────────────────────────────────────────────────────
// Automation Overlay — a separate, transparent, topmost floating window
// that shows real-time automation steps while the agent controls the desktop.
// ─────────────────────────────────────────────────────────────────────────────

const (
	autoOverlayW = 320
	autoOverlayH = 60 // starts small, expands with steps
)

var (
	autoView      webview.WebView
	autoHWND      uintptr
	autoMutex     sync.Mutex
	autoSteps     []string
	autoVisible   bool
	autoHideTimer *time.Timer
	autoOnce      sync.Once
)

// ensureAutomationOverlay lazily starts the automation overlay WebView on
// first use, replacing the old eager startup goroutine in main.go.
func ensureAutomationOverlay() {
	autoOnce.Do(func() { go RunAutomationOverlay() })
}

const autoHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8"/>
<style>
  @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&display=swap');
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body {
    background: transparent;
    width: 100%; height: 100%;
    font-family: 'Inter', -apple-system, sans-serif;
    overflow: hidden;
    padding-top: 2px;
  }
  .card {
    background: rgba(18, 18, 22, 0.75);
    backdrop-filter: blur(24px);
    -webkit-backdrop-filter: blur(24px);
    border: 1px solid rgba(255, 255, 255, 0.12);
    border-radius: 24px;
    padding: 16px 20px;
    margin: 0 auto;
    width: 95%;
    box-shadow: 0 8px 32px rgba(0,0,0,0.4);
    opacity: 0;
    transform: translateY(-10px) scale(0.98);
    transition: all 0.4s cubic-bezier(0.16, 1, 0.3, 1);
  }
  .card.visible { opacity: 1; transform: translateY(0) scale(1); }
  .header {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;
  }
  .dot {
    width: 10px; height: 10px;
    border-radius: 50%;
    background: #4f9cff;
    box-shadow: 0 0 10px rgba(79, 156, 255, 0.8);
    animation: pulse 1.5s ease-in-out infinite;
  }
  @keyframes pulse {
    0%,100% { opacity: 1; transform: scale(1); }
    50% { opacity: 0.6; transform: scale(0.85); }
  }
  .title {
    font-size: 13px;
    font-weight: 600;
    color: rgba(255,255,255,0.95);
    letter-spacing: 0.03em;
  }
  .steps {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .step {
    font-size: 12px;
    color: rgba(255,255,255,0.6);
    padding-left: 6px;
    display: flex;
    align-items: flex-start;
    gap: 8px;
    animation: fadeIn 0.3s ease;
    line-height: 1.4;
  }
  .step::before { content: '•'; color: rgba(79, 156, 255, 0.8); font-size: 14px; margin-top: -1px; }
  .step.latest { color: rgba(255,255,255,0.95); }
  .step.latest::before { color: #4f9cff; }
  @keyframes fadeIn { from { opacity:0; transform:translateY(-4px); } to { opacity:1; transform:none; } }
  .done .dot { background: #22c55e; box-shadow: 0 0 10px rgba(34, 197, 94, 0.8); animation: none; }
  .done .title { color: #22c55e; }
  .error .dot { background: #ef4444; box-shadow: 0 0 10px rgba(239, 68, 68, 0.8); animation: none; }
  .error .title { color: #ef4444; }
</style>
</head>
<body>
<div class="card" id="card">
  <div class="header">
    <div class="dot" id="dot"></div>
    <span class="title" id="title">Automation Running</span>
  </div>
  <div class="steps" id="steps"></div>
</div>
<script>
  function addStep(text) {
    const steps = document.getElementById('steps');
    // de-bold previous latest
    const prev = steps.querySelector('.latest');
    if (prev) prev.classList.remove('latest');

    const div = document.createElement('div');
    div.className = 'step latest';
    div.textContent = text;
    steps.appendChild(div);
    // keep last 5 steps visible
    while (steps.children.length > 5) steps.removeChild(steps.firstChild);
    window.scrollTo({ top: document.body.scrollHeight, behavior: 'smooth' });
  }
  function setDone() {
    document.getElementById('card').classList.add('done');
    document.getElementById('title').textContent = 'Completed';
  }
  function setError() {
    document.getElementById('card').classList.add('error');
    document.getElementById('title').textContent = 'Automation Failed';
  }
  function show() {
    document.getElementById('card').classList.add('visible');
  }
  function hide() {
    document.getElementById('card').classList.remove('visible');
    document.getElementById('steps').innerHTML = '';
    document.getElementById('card').className = 'card';
    document.getElementById('title').textContent = 'Automation Running';
  }
</script>
</body>
</html>`

// RunAutomationOverlay starts the automation overlay WebView window.
// Call this from a dedicated goroutine — it blocks until the window closes.
func RunAutomationOverlay() {
	autoView = webview.New(false)
	autoView.SetTitle("")
	autoView.SetSize(autoOverlayW, autoOverlayH, webview.HintFixed)
	autoView.SetHtml(autoHTML)

	// Apply Win32 window flags after a short delay
	go func() {
		time.Sleep(300 * time.Millisecond)
		autoView.Dispatch(func() {
			applyAutoOverlayStyle()
		})
	}()

	autoView.Run()
}

func applyAutoOverlayStyle() {
	hwnd := uintptr(autoView.Window())
	if hwnd == 0 {
		return
	}
	autoHWND = hwnd

	moduser32dll := syscall.NewLazyDLL("user32.dll")
	getWL := moduser32dll.NewProc("GetWindowLongPtrW")
	setWL := moduser32dll.NewProc("SetWindowLongPtrW")
	setWP := moduser32dll.NewProc("SetWindowPos")
	getSM := moduser32dll.NewProc("GetSystemMetrics")

	// Remove borders / title bar
	style, _, _ := getWL.Call(hwnd, uintptr(uint32(0xFFFFFFF0)))
	style = style &^ (0x00C00000 | 0x00040000 | 0x00020000 | 0x00010000 | 0x00080000)
	style |= 0x80000000 // WS_POPUP
	setWL.Call(hwnd, uintptr(uint32(0xFFFFFFF0)), style)

	// TOPMOST + TOOLWINDOW + LAYERED (for transparency)
	exStyle, _, _ := getWL.Call(hwnd, uintptr(uint32(0xFFFFFFEC)))
	exStyle |= 0x00000008 | 0x00000080 | 0x00080000 // TOPMOST | TOOLWINDOW | LAYERED
	setWL.Call(hwnd, uintptr(uint32(0xFFFFFFEC)), exStyle)

	// Position top-center, hidden initially (off-screen to the top)
	sw, _, _ := getSM.Call(0)
	x := (int(sw) - autoOverlayW) / 2
	setWP.Call(hwnd, ^uintptr(0), uintptr(x), uintptr(0xFFFF), uintptr(autoOverlayW), uintptr(autoOverlayH), 0x0020|0x0040)
}

// ShowAutomationStep displays a step message in the automation overlay.
// Safe to call from any goroutine.
func ShowAutomationStep(step string) {
	ensureAutomationOverlay()
	fmt.Printf("🤖 [AUTO] %s\n", step)
	if autoView == nil {
		return
	}

	autoMutex.Lock()
	autoSteps = append(autoSteps, step)
	height := 60 + len(autoSteps)*22
	if height > 260 {
		height = 260
	}
	autoMutex.Unlock()

	// Cancel any pending hide timer
	if autoHideTimer != nil {
		autoHideTimer.Stop()
	}

	escaped, _ := json.Marshal(step)
	autoView.Dispatch(func() {
		if !autoVisible {
			// Slide window into view
			moduser32dll := syscall.NewLazyDLL("user32.dll")
			getSM := moduser32dll.NewProc("GetSystemMetrics")
			setWP := moduser32dll.NewProc("SetWindowPos")
			sw, _, _ := getSM.Call(0)
			x := (int(sw) - autoOverlayW) / 2
			setWP.Call(autoHWND, ^uintptr(0), uintptr(x), 10, uintptr(autoOverlayW), uintptr(height), 0x0020|0x0040)
			autoView.Eval("show();")
			autoVisible = true
		}
		autoView.Eval(fmt.Sprintf("addStep(%s);", string(escaped)))
	})

	// Schedule auto-hide 3 seconds after last step
	autoHideTimer = time.AfterFunc(3*time.Second, func() {
		HideAutomationOverlay(false)
	})
}

// HideAutomationOverlay hides the overlay. isError controls the final badge.
func HideAutomationOverlay(isError bool) {
	if autoView == nil {
		return
	}
	autoView.Dispatch(func() {
		if isError {
			autoView.Eval("setError();")
		} else {
			autoView.Eval("setDone();")
		}
		time.Sleep(1200 * time.Millisecond)
		autoView.Eval("hide();")

		// Slide off screen
		if autoHWND != 0 {
			moduser32dll := syscall.NewLazyDLL("user32.dll")
			setWP := moduser32dll.NewProc("SetWindowPos")
			setWP.Call(autoHWND, ^uintptr(0), 0, 0xFFFF, uintptr(autoOverlayW), uintptr(autoOverlayH), 0x0002|0x0040)
		}

		autoMutex.Lock()
		autoSteps = nil
		autoVisible = false
		autoMutex.Unlock()
	})
}
