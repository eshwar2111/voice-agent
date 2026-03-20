package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/lxn/win"
	webview "github.com/webview/webview_go"
)

var (
	moduser32 = syscall.NewLazyDLL("user32.dll")
	modgdi32  = syscall.NewLazyDLL("gdi32.dll")

	procGetSystemMetrics   = moduser32.NewProc("GetSystemMetrics")
	procSetWindowPos       = moduser32.NewProc("SetWindowPos")
	procSetWindowRgn       = moduser32.NewProc("SetWindowRgn")
	procCreateRoundRectRgn = modgdi32.NewProc("CreateRoundRectRgn")
)

type AgentState int

const (
	StateBoot AgentState = iota
	StateIdle
	StateListening
	StateReasoning
	StateExecuting
	StateSpeaking
)

const (
	// Default Pill Dimensions
	defaultW = 240
	defaultH = 44
)

var (
	currentState  AgentState = StateBoot
	stateMutex    sync.Mutex
	w             webview.WebView
	hwndGlobal    win.HWND
	ListenTrigger = make(chan struct{})

	notifTimer *time.Timer

	confirmChan chan bool
)

func init() {
	// Force WebView2 background to be transparent (0 alpha)
	// This must be set before the webview is initialized.
	os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "0")
	confirmChan = make(chan bool)
}

// ─────────────────────────────────────────────────────────────────────────────
// HTML / CSS / JS
// ─────────────────────────────────────────────────────────────────────────────
const htmlTemplate = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8"/>
<style>
  @import url('https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700&display=swap');
  
  * { box-sizing: border-box; margin: 0; padding: 0; }

  html, body {
    background: #0a0a0c !important; /* Premium dark background */
    overflow: hidden;
    user-select: none;
    font-family: 'Plus Jakarta Sans', sans-serif;
    color: white;
    width: 100vw; height: 100vh;
    display: flex;
    flex-direction: column;
  }

  /* ── Header Pill Area ── */
  #header {
    width: 100%; height: 44px;
    display: flex; align-items: center; justify-content: space-between;
    padding: 0 16px;
    flex-shrink: 0;
    cursor: pointer;
    border-bottom: 1px solid rgba(255,255,255,0.05);
  }
  #header:hover { background: rgba(255, 255, 255, 0.03); }

  #status-dot {
    width: 8px; height: 8px; border-radius: 50%;
    background: #22c55e;
    box-shadow: 0 0 10px rgba(34, 197, 94, 0.4);
    margin-right: 12px;
  }

  #status-text {
    font-size: 13px; font-weight: 600;
    color: rgba(255, 255, 255, 0.95);
    flex: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }

  #header-actions {
    display: flex; align-items: center; gap: 8px;
  }

  #close-btn {
    display: none; width: 20px; height: 20px;
    align-items: center; justify-content: center;
    border-radius: 50%; background: rgba(255,255,255,0.1);
    color: rgba(255,255,255,0.6); cursor: pointer;
    font-size: 12px; transition: all 0.2s;
  }
  #close-btn:hover { background: rgba(255,255,255,0.2); color: #fff; }

  #visualizer { display: none; align-items: center; gap: 3px; height: 16px; }
  .bar { width: 3px; border-radius: 1.5px; background: #fff; animation: bounce 0.6s infinite alternate; }
  @keyframes bounce { 0% { height: 4px; } 100% { height: 16px; } }

  .loader {
    display: none; width: 14px; height: 14px;
    border: 2px solid rgba(255, 255, 255, 0.1);
    border-top-color: #38bdf8;
    border-radius: 50%;
    animation: rotate 0.8s linear infinite;
  }
  @keyframes rotate { to { transform: rotate(360deg); } }

  /* ── Command / Card Sections ── */
  #cmd-container { display: none; padding: 14px 16px; animation: slideDown 0.3s ease; }
  #cmd-input {
    width: 100%; background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 10px; color: #fff; padding: 10px 12px;
    font-size: 14px; font-family: inherit; outline: none;
  }
  #cmd-input:focus { border-color: #38bdf8; background: rgba(255, 255, 255, 0.08); }

  #card-content { display: none; flex-direction: column; padding: 14px 16px; flex: 1; animation: slideDown 0.3s ease; }
  #card-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px; }
  #card-label { font-size: 10px; font-weight: 800; color: #64748b; text-transform: uppercase; }
  #copy-btn {
    font-size: 10px; font-weight: 700; color: #38bdf8; cursor: pointer;
    text-transform: uppercase; padding: 2px 6px; border-radius: 4px;
    background: rgba(56, 189, 248, 0.1); transition: all 0.2s;
  }
  #copy-btn:hover { background: rgba(56, 189, 248, 0.2); }
  #card-body { 
    font-size: 14px; line-height: 1.6; color: #e2e8f0; 
    overflow-y: auto; max-height: 280px; padding-right: 4px;
  }
  #card-body::-webkit-scrollbar { width: 4px; }
  #card-body::-webkit-scrollbar-thumb { background: rgba(255,255,255,0.1); border-radius: 2px; }

  /* ── Confirmation Section ── */
  #confirm-container { display: none; flex-direction: column; padding: 14px 16px; flex: 1; animation: slideDown 0.3s ease; }
  #confirm-msg { font-size: 14px; color: #fff; margin-bottom: 16px; line-height: 1.4; }
  #confirm-actions { display: flex; gap: 10px; }
  .btn {
    flex: 1; padding: 10px; border-radius: 10px; font-size: 13px; font-weight: 600;
    cursor: pointer; text-align: center; transition: all 0.2s; border: none; outline: none;
  }
  .btn-approve { background: #22c55e; color: #000; }
  .btn-approve:hover { background: #16a34a; transform: translateY(-1px); }
  .btn-cancel { background: rgba(255,255,255,0.1); color: #fff; }
  .btn-cancel:hover { background: rgba(255,255,255,0.15); }

  @keyframes slideDown { from { opacity: 0; transform: translateY(-10px); } to { opacity: 1; transform: translateY(0); } }
</style>

<script>
  let isExpanded = false;

  function updateUI(state, text) {
    const dot = document.getElementById('status-dot');
    const label = document.getElementById('status-text');
    const viz = document.getElementById('visualizer');
    const load = document.querySelector('.loader');

    viz.style.display = 'none';
    load.style.display = 'none';

    const cfg = {
      idle:      { c: '#22c55e', l: 'Ready', w: 240, h: 44 },
      listening: { c: '#ef4444', l: 'Listening...', v: true, w: 280, h: 44 },
      thinking:  { c: '#38bdf8', l: 'Thinking...', s: true, w: 260, h: 44 },
      acting:    { c: '#f59e0b', l: 'Acting...', s: true, w: 260, h: 44 },
      speaking:  { c: '#f97316', l: 'Speaking...', v: true, w: 260, h: 44 }
    }[state] || { c: '#22c55e', l: 'Ready', w: 240, h: 44 };

    dot.style.background = cfg.c;
    label.innerText = text || cfg.l;
    if (cfg.v) viz.style.display = 'flex';
    if (cfg.s) load.style.display = 'block';

    if (!isExpanded) {
      window.callResize(cfg.w, cfg.h, 22);
    }
  }

  function showCommand() {
    isExpanded = true;
    hideAllContainers();
    document.getElementById('cmd-container').style.display = 'block';
    document.getElementById('close-btn').style.display = 'flex';
    window.callResize(360, 100, 16);
    setTimeout(() => document.getElementById('cmd-input').focus(), 50);
  }

  function showCard(content) {
    isExpanded = true;
    hideAllContainers();
    document.getElementById('card-content').style.display = 'flex';
    document.getElementById('close-btn').style.display = 'flex';
    document.getElementById('card-body').innerText = content;
    window.callResize(380, 360, 20);
  }

  function showConfirm(msg) {
    isExpanded = true;
    hideAllContainers();
    document.getElementById('confirm-container').style.display = 'flex';
    document.getElementById('close-btn').style.display = 'flex';
    document.getElementById('confirm-msg').innerText = msg;
    window.callResize(320, 150, 20);
  }

  function hideAllContainers() {
    document.getElementById('cmd-container').style.display = 'none';
    document.getElementById('card-content').style.display = 'none';
    document.getElementById('confirm-container').style.display = 'none';
  }

  function resetUI() {
    isExpanded = false;
    hideAllContainers();
    document.getElementById('close-btn').style.display = 'none';
    updateUI('idle');
  }

  function handleHeaderClick() {
    if (isExpanded) resetUI();
    else window.triggerListen();
  }

  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && isExpanded) {
      resetUI();
    }
  });

  function handleKey(e) {
    if (e.key === 'Enter') { 
      window.submitCommand(e.target.value); 
      e.target.value=''; 
      resetUI(); 
    }
  }

  function approve() { window.confirmCallback(true); resetUI(); }
  function cancel() { window.confirmCallback(false); resetUI(); }
</script>
</head>
<body oncontextmenu="return false;">
  <div id="header" onclick="handleHeaderClick()">
    <div id="status-dot"></div>
    <div id="status-text">Ready</div>
    <div id="header-actions">
      <div id="visualizer"><div class="bar"></div><div class="bar"></div><div class="bar"></div></div>
      <div class="loader"></div>
      <div id="close-btn" onclick="event.stopPropagation(); resetUI();">✕</div>
    </div>
  </div>
  <div id="cmd-container"><input type="text" id="cmd-input" placeholder="Type a command..." onkeydown="handleKey(event)"></div>
  <div id="card-content">
    <div id="card-header">
      <div id="card-label">Assistant</div>
      <div id="copy-btn" onclick="copyCardText()">Copy</div>
    </div>
    <div id="card-body"></div>
  </div>
  <div id="confirm-container">
    <div id="confirm-msg"></div>
    <div id="confirm-actions">
      <button class="btn btn-approve" onclick="approve()">Approve</button>
      <button class="btn btn-cancel" onclick="cancel()">Cancel</button>
    </div>
  </div>
</body>
<script>
  function copyCardText() {
    const text = document.getElementById('card-body').innerText;
    navigator.clipboard.writeText(text).then(() => {
      const btn = document.getElementById('copy-btn');
      btn.innerText = 'Copied!';
      setTimeout(() => btn.innerText = 'Copy', 2000);
    });
  }
</script>
</html>`

func SetState(s AgentState) {
	stateMutex.Lock()
	defer stateMutex.Unlock()
	currentState = s
	if w != nil {
		sk := stateKey(s)
		w.Dispatch(func() {
			w.Eval(fmt.Sprintf("updateUI('%s');", sk))
		})
	}
}

func stateKey(s AgentState) string {
	switch s {
	case StateIdle:
		return "idle"
	case StateListening:
		return "listening"
	case StateReasoning:
		return "thinking"
	case StateExecuting:
		return "acting"
	case StateSpeaking:
		return "speaking"
	}
	return "boot"
}

func ShowNotification(text string) {
	if w == nil {
		return
	}
	escaped, _ := json.Marshal(text)
	w.Dispatch(func() {
		w.Eval(fmt.Sprintf("updateUI('idle', %s);", string(escaped)))
	})
	if notifTimer != nil {
		notifTimer.Stop()
	}
	notifTimer = time.AfterFunc(4*time.Second, func() {
		w.Dispatch(func() { w.Eval("updateUI('idle');") })
	})
}

func ShowCommandBarInOverlay() {
	if w == nil {
		return
	}
	w.Dispatch(func() { w.Eval("showCommand();") })
}

func ShowOutputOverlay(text string) {
	if w == nil {
		return
	}
	if len(text) < 55 {
		ShowNotification(text)
		return
	}
	escaped, _ := json.Marshal(text)
	w.Dispatch(func() { w.Eval(fmt.Sprintf("showCard(%s);", string(escaped))) })
}

func RequestConfirmation(msg string) bool {
	if w == nil {
		return false
	}
	escaped, _ := json.Marshal(msg)
	w.Dispatch(func() { w.Eval(fmt.Sprintf("showConfirm(%s);", string(escaped))) })

	// Block until confirmChan receives a value
	return <-confirmChan
}

func resizeWindow(width, height, radius int) {
	if hwndGlobal == 0 {
		return
	}
	sw := win.GetSystemMetrics(win.SM_CXSCREEN)
	x := (int(sw) - width) / 2

	// 1. Physically resize and reposition the window
	win.SetWindowPos(hwndGlobal, win.HWND_TOPMOST, int32(x), 10, int32(width), int32(height), win.SWP_NOACTIVATE)

	// 2. Create a rounded rectangle region and APPLY it to the window
	// This "clips" the window so only the rounded area exists.
	// This is the most reliable way to get a pill shape on Windows.
	hrgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(width), uintptr(height), uintptr(radius*2), uintptr(radius*2))
	procSetWindowRgn.Call(uintptr(hwndGlobal), hrgn, 1)
}

func StartOverlay() {
	w = webview.NewWindow(false, nil)
	defer w.Destroy()

	w.SetTitle("Voice Agent")
	w.SetSize(defaultW, defaultH, webview.HintNone)

	w.Bind("triggerListen", func() { ListenTrigger <- struct{}{} })
	w.Bind("submitCommand", func(input string) {
		if OnCommand != nil {
			OnCommand(input)
		}
	})
	w.Bind("confirmCallback", func(approved bool) { confirmChan <- approved })
	w.Bind("callResize", func(width, height, radius int) {
		w.Dispatch(func() { resizeWindow(width, height, radius) })
	})

	w.SetHtml(htmlTemplate)

	go func() {
		time.Sleep(250 * time.Millisecond)
		w.Dispatch(func() {
			hwnd := win.HWND(w.Window())
			hwndGlobal = hwnd

			// Remove borders/title bar
			style := win.GetWindowLong(hwnd, win.GWL_STYLE)
			win.SetWindowLong(hwnd, win.GWL_STYLE, style&^(win.WS_CAPTION|win.WS_THICKFRAME))

			// Add Topmost and Toolwindow (no taskbar icon)
			exStyle := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
			win.SetWindowLong(hwnd, win.GWL_EXSTYLE, exStyle|win.WS_EX_TOPMOST|win.WS_EX_TOOLWINDOW)

			// Initial Pill Clip
			resizeWindow(defaultW, defaultH, 22)
		})
	}()

	w.Run()
}
