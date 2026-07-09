package ui

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	webview "github.com/webview/webview_go"
	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
)

var (
	moduser32 = syscall.NewLazyDLL("user32.dll")
	modgdi32  = syscall.NewLazyDLL("gdi32.dll")

	procGetSystemMetrics   = moduser32.NewProc("GetSystemMetrics")
	procSetWindowPos       = moduser32.NewProc("SetWindowPos")
	procSetWindowRgn       = moduser32.NewProc("SetWindowRgn")
	procCreateRoundRectRgn = modgdi32.NewProc("CreateRoundRectRgn")
	procGetWindowRect      = moduser32.NewProc("GetWindowRect")
	procGetDpiForWindow    = moduser32.NewProc("GetDpiForWindow")
	procRedrawWindow       = moduser32.NewProc("RedrawWindow")

	modkernel32          = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentThread = modkernel32.NewProc("GetCurrentThreadId")
)

// dpiScale returns the window's DPI scale factor (1.0 at 96 DPI / 100%).
func dpiScale() float64 {
	if hwndGlobal == 0 || procGetDpiForWindow.Find() != nil {
		return 1
	}
	dpi, _, _ := procGetDpiForWindow.Call(uintptr(hwndGlobal))
	if dpi == 0 {
		return 1
	}
	return float64(dpi) / 96.0
}

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
	defaultW = 300
	defaultH = 52
)

var (
	currentState  AgentState = StateBoot
	stateMutex    sync.Mutex
	w             webview.WebView
	hwndGlobal    win.HWND
	ListenTrigger = make(chan struct{})

	notifTimer *time.Timer

	confirmChan chan bool

	// OnCommand is declared in command_bar.go
	OnSettingsSaved func(cfg interface{})
)

//go:embed overlay_v2.html
var htmlTemplate string

func init() {
	// Force WebView2 background to be transparent (0 alpha)
	// This must be set before the webview is initialized.
	os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "0")
	confirmChan = make(chan bool)
}

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

func GetCurrentState() AgentState {
	stateMutex.Lock()
	defer stateMutex.Unlock()
	return currentState
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

func SetMeetingAlert(title string, minutes int) {
	if w == nil {
		return
	}
	escapedTitle, _ := json.Marshal(title)
	text := fmt.Sprintf("in %d mins", minutes)
	escapedText, _ := json.Marshal(text)
	w.Dispatch(func() {
		w.Eval(fmt.Sprintf("triggerMeetingAlert(%s, %s);", string(escapedTitle), string(escapedText)))
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

func RequestConfirmationCard(cardJSON string) bool {
	if w == nil {
		return false
	}
	w.Dispatch(func() {
		w.Eval(fmt.Sprintf("showConfirmCard(%s);", cardJSON))
	})
	return <-confirmChan
}

func RequestConfirmation(msg string) bool {
	if w == nil {
		return false
	}
	escaped, _ := json.Marshal(msg)
	w.Dispatch(func() { w.Eval(fmt.Sprintf("showConfirm(%s);", string(escaped))) })
	return <-confirmChan
}

// LowerTopmostForOAuth temporarily removes TOPMOST so the OAuth browser can be focused.
func LowerTopmostForOAuth() {
	if hwndGlobal == 0 {
		return
	}
	exStyle := win.GetWindowLong(hwndGlobal, win.GWL_EXSTYLE)
	// Remove TOPMOST, keep other styles
	win.SetWindowLong(hwndGlobal, win.GWL_EXSTYLE, exStyle&^win.WS_EX_TOPMOST)
	win.SetWindowPos(hwndGlobal, win.HWND_NOTOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
}

// RestoreTopmost puts the overlay back on top after the OAuth browser is done.
func RestoreTopmost() {
	if hwndGlobal == 0 {
		return
	}
	exStyle := win.GetWindowLong(hwndGlobal, win.GWL_EXSTYLE)
	win.SetWindowLong(hwndGlobal, win.GWL_EXSTYLE, exStyle|win.WS_EX_TOPMOST)
	win.SetWindowPos(hwndGlobal, win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
}

func resizeWindow(width, height, radius int) {
	if hwndGlobal == 0 {
		log.Printf("[ui/resize] SKIP: hwnd not ready (req %dx%d)", width, height)
		return
	}
	// The width/height/radius passed in are CSS pixels (the design's logical
	// units, matching the VIEW map in overlay_v2.html). WebView2 renders CSS at
	// the system DPI scale, so the physical window must be scaled by the same
	// factor — otherwise at 125% the whole UI renders ~20% too small and the
	// content viewport won't match the authored layout.
	scale := dpiScale()
	pw := int(float64(width) * scale)
	ph := int(float64(height) * scale)
	pr := int(float64(radius) * scale)
	sw := win.GetSystemMetrics(win.SM_CXSCREEN)
	x := (int(sw) - pw) / 2

	// 1. Physically resize and reposition the window (physical pixels)
	win.SetWindowPos(hwndGlobal, win.HWND_TOPMOST, int32(x), int32(10*scale), int32(pw), int32(ph), win.SWP_NOACTIVATE)

	// 2. Create a rounded rectangle region and APPLY it to the window
	hrgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(pw+1), uintptr(ph+1), uintptr(pr*2), uintptr(pr*2))
	procSetWindowRgn.Call(uintptr(hwndGlobal), hrgn, 1)

	// 3. Force a full repaint (frame + all children). WebView2 hosts its browser
	// in a child HWND and leaves stale pixels behind when the window shrinks —
	// that's the "grey ghost" at the old size. RDW_INVALIDATE|ERASE|ALLCHILDREN|
	// UPDATENOW|FRAME (0x0585) clears it immediately.
	procRedrawWindow.Call(uintptr(hwndGlobal), 0, 0, 0x0585)

	// diagnostics — actual window rect after the resize
	var rc struct{ L, T, R, B int32 }
	procGetWindowRect.Call(uintptr(hwndGlobal), uintptr(unsafe.Pointer(&rc)))
	log.Printf("[ui/resize] req=%dx%d rad=%d dpiScale=%.2f -> actual window rect=%dx%d at (%d,%d)",
		width, height, radius, scale, rc.R-rc.L, rc.B-rc.T, rc.L, rc.T)
}

// SetInputActive toggles whether the overlay can take keyboard focus. The pill is
// created WS_EX_NOACTIVATE so it never steals focus; but typing panels (command bar,
// settings) need real keyboard focus, so we clear that flag and foreground the window
// while they're open, then restore no-activate when they close.
func SetInputActive(active bool) {
	if hwndGlobal == 0 {
		return
	}
	exStyle := win.GetWindowLong(hwndGlobal, win.GWL_EXSTYLE)
	if active {
		win.SetWindowLong(hwndGlobal, win.GWL_EXSTYLE, exStyle&^win.WS_EX_NOACTIVATE)
		// Windows blocks SetForegroundWindow from a background/no-activate process.
		// Attach to the current foreground thread's input queue so the call is honored,
		// then set foreground + keyboard focus, then detach.
		fg := win.GetForegroundWindow()
		fgThread := win.GetWindowThreadProcessId(fg, nil)
		myThread, _, _ := procGetCurrentThread.Call()
		win.AttachThreadInput(int32(fgThread), int32(myThread), true)
		win.BringWindowToTop(hwndGlobal)
		win.SetForegroundWindow(hwndGlobal)
		win.SetFocus(hwndGlobal)
		win.AttachThreadInput(int32(fgThread), int32(myThread), false)
		got := win.GetForegroundWindow() == hwndGlobal
		log.Printf("[ui] input ACTIVE — overlay is now foreground=%v", got)
	} else {
		win.SetWindowLong(hwndGlobal, win.GWL_EXSTYLE, exStyle|win.WS_EX_NOACTIVATE)
		log.Println("[ui] input inactive (no-activate restored)")
	}
}

func StartOverlay(ctx context.Context, cfg *config.Config) {
	w = webview.NewWindow(false, nil)
	defer w.Destroy()

	w.SetTitle("Voice Agent")
	w.SetSize(defaultW, defaultH, webview.HintNone)

	w.Bind("triggerListen", func() { ListenTrigger <- struct{}{} })
	w.Bind("submitCommand", func(input string) {
		// Run the dispatch OFF the WebView thread. OnCommand blocks on the
		// LLM/tool pipeline; if it ran here it would freeze the WebView main
		// loop, so every w.Dispatch'd resize (e.g. shrink-back-to-pill after
		// submit) would queue behind it — leaving the pill stuck at command
		// size showing "Dispatching…". A goroutine frees the loop immediately.
		if OnCommand != nil {
			go OnCommand(input)
		}
	})
	w.Bind("getPrevCommand", func() string {
		if OnHistoryUp != nil {
			return OnHistoryUp()
		}
		return ""
	})
	w.Bind("getNextCommand", func() string {
		if OnHistoryDown != nil {
			return OnHistoryDown()
		}
		return ""
	})

	setupAuthBindings(ctx, cfg, w)

	w.Bind("confirmCallback", func(approved bool) { confirmChan <- approved })
	w.Bind("suggestionAccept", func(id string) {
		if OnSuggestionAccept != nil {
			OnSuggestionAccept(id)
		}
	})
	w.Bind("suggestionDismiss", func(id string) {
		if OnSuggestionDismiss != nil {
			OnSuggestionDismiss(id)
		}
	})
	w.Bind("callResize", func(width, height, radius int) {
		w.Dispatch(func() { resizeWindow(width, height, radius) })
	})
	w.Bind("setInputActive", func(active bool) {
		w.Dispatch(func() { SetInputActive(active) })
	})
	w.Bind("quitApp", func() {
		log.Println("[ui] quit requested from UI")
		w.Dispatch(func() { w.Terminate() })
	})
	w.Bind("jslog", func(msg string) { log.Printf("%s", msg) })
	w.Bind("getSettings", func() map[string]interface{} {
		return map[string]interface{}{
			"llm_provider": cfg.LLMProvider,
			"api_key":      cfg.APIKey,
			"model":        cfg.Model,
			"base_url":     cfg.BaseURL,
			"enable_voice": cfg.EnableVoice,
			"privacy_mode": cfg.PrivacyMode,
		}
	})
	w.Bind("saveSettings", func(provider, apiKey, model, baseURL string, enableVoice, privacyMode bool) bool {
		if provider != "" {
			cfg.LLMProvider = provider
		}
		cfg.APIKey = apiKey
		cfg.Model = model
		cfg.BaseURL = baseURL
		cfg.EnableVoice = enableVoice
		cfg.PrivacyMode = privacyMode
		if err := config.SaveConfig("config.json", cfg); err != nil {
			fmt.Printf("Save settings error: %v\n", err)
			return false
		}
		return true
	})

	w.SetHtml(htmlTemplate)

	go func() {
		time.Sleep(250 * time.Millisecond)
		w.Dispatch(func() {
			hwnd := win.HWND(w.Window())
			hwndGlobal = hwnd

			style := win.GetWindowLong(hwnd, win.GWL_STYLE)
			win.SetWindowLong(hwnd, win.GWL_STYLE, style&^(win.WS_CAPTION|win.WS_THICKFRAME))

			exStyle := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
			win.SetWindowLong(hwnd, win.GWL_EXSTYLE, exStyle|win.WS_EX_TOPMOST|win.WS_EX_TOOLWINDOW|win.WS_EX_NOACTIVATE)

			resizeWindow(defaultW, defaultH, 26)
		})
	}()

	w.Run()
}

func setupAuthBindings(ctx context.Context, cfg *config.Config, w webview.WebView) {
	w.Bind("linkGoogle", func() {
		go func() {
			LowerTopmostForOAuth()
			err := auth.StartGoogleAuth(ctx, cfg)
			RestoreTopmost()
			if err != nil {
				fmt.Printf("Google Auth Error: %v\n", err)
			} else {
				// Refresh integration status in UI after successful link
				w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
			}
		}()
	})

	w.Bind("unlinkGoogle", func() {
		cfg.GoogleToken = ""
		config.SaveConfig("config.json", cfg)
		w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
	})

	w.Bind("getGoogleStatus", func() map[string]interface{} {
		store := auth.NewTokenStore("config.json")
		_, err := store.LoadToken(auth.ProviderGoogle, cfg)
		if err != nil {
			return map[string]interface{}{"connected": false}
		}
		res := map[string]interface{}{
			"connected":    true,
			"workspace":    []string{"Gmail", "Calendar", "Drive", "Docs", "Sheets", "Slides"},
			"capabilities": 6,
		}
		if info, err := auth.GetGoogleUserInfo(ctx, cfg); err == nil && info != nil {
			res["email"] = info.Email
		}
		return res
	})

	w.Bind("linkMicrosoft", func() {
		go func() {
			LowerTopmostForOAuth()
			err := auth.StartMicrosoftAuth(ctx, cfg)
			RestoreTopmost()
			if err != nil {
				fmt.Printf("Microsoft Auth Error: %v\n", err)
			} else {
				w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
			}
		}()
	})

	w.Bind("unlinkMicrosoft", func() {
		cfg.MicrosoftToken = ""
		config.SaveConfig("config.json", cfg)
		w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
	})

	w.Bind("getMicrosoftStatus", func() map[string]interface{} {
		store := auth.NewTokenStore("config.json")
		_, err := store.LoadToken(auth.ProviderMicrosoft, cfg)
		if err != nil {
			return map[string]interface{}{"connected": false}
		}
		return map[string]interface{}{"connected": true, "workspace": []string{"Outlook", "Calendar", "OneDrive"}}
	})

	w.Bind("linkSpotify", func() {
		go func() {
			LowerTopmostForOAuth()
			err := auth.StartSpotifyAuth(ctx, cfg)
			RestoreTopmost()
			if err != nil {
				fmt.Printf("Spotify Auth Error: %v\n", err)
			} else {
				w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
			}
		}()
	})

	w.Bind("unlinkSpotify", func() {
		cfg.SpotifyToken = ""
		config.SaveConfig("config.json", cfg)
		w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
	})

	w.Bind("getSpotifyStatus", func() map[string]interface{} {
		store := auth.NewTokenStore("config.json")
		_, err := store.LoadToken(auth.ProviderSpotify, cfg)
		if err != nil {
			return map[string]interface{}{"connected": false}
		}
		res := map[string]interface{}{
			"connected":    true,
			"capabilities": []string{"Playback", "Queue", "Recommendations", "AI Curation"},
		}
		if info, err := auth.GetSpotifyUserInfo(ctx, cfg); err == nil && info != nil {
			res["display_name"] = info.DisplayName
			res["product"] = info.Product
		}
		return res
	})
}
