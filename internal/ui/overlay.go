package ui

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"syscall"
	"time"

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
	procGetDpiForWindow    = moduser32.NewProc("GetDpiForWindow")

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

var (
	currentState  AgentState = StateBoot
	stateMutex    sync.Mutex
	w             webview.WebView
	hwndGlobal    win.HWND
	canvasGlobal  *canvas
	bridge        *Bridge
	ListenTrigger = make(chan struct{})

	notifTimer *time.Timer

	confirmChan chan bool

	// OnCommand is declared in command_bar.go
	OnSettingsSaved func(cfg interface{})
)

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
		bridge.Push("state", map[string]string{"state": sk})
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
	bridge.Push("notify", map[string]string{"text": text})
	if notifTimer != nil {
		notifTimer.Stop()
	}
	notifTimer = time.AfterFunc(4*time.Second, func() {
		bridge.Push("notify", map[string]string{"text": ""})
	})
}

func SetMeetingAlert(title string, minutes int) {
	if w == nil {
		return
	}
	text := fmt.Sprintf("in %d mins", minutes)
	bridge.Push("activity:update", map[string]any{
		"id": "ambient.nudge",
		"data": map[string]any{
			"id":      "meeting",
			"icon":    "calendar",
			"title":   title,
			"message": text,
			"action":  "",
		},
	})
}

func ShowCommandBarInOverlay() {
	if w == nil {
		return
	}
	bridge.Push("surface:open", map[string]string{"id": "command"})
}

func ShowOutputOverlay(text string) {
	if w == nil {
		return
	}
	if len(text) < 55 {
		ShowNotification(text)
		return
	}
	bridge.Push("surface:open", map[string]any{"id": "result", "text": text})
}

func RequestConfirmationCard(cardJSON string) bool {
	if w == nil {
		return false
	}
	bridge.Push("surface:open", map[string]any{"id": "approve", "card": cardJSON})
	return <-confirmChan
}

func RequestConfirmation(msg string) bool {
	if w == nil {
		return false
	}
	bridge.Push("surface:open", map[string]any{"id": "approve", "text": msg})
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

	bridge = newBridge(w)
	canvasGlobal = newCanvas(w)
	// Shape and place the window BEFORE it is ever painted. Doing this from the
	// uiReady callback means the user sees a default-styled, unshaped, wrongly
	// sized window until JS finishes loading — and w.SetSize before Attach()
	// strips WS_CAPTION/WS_THICKFRAME would resize the window a *second* time
	// (frame-sized vs. client-sized), which is the exact relayout jank this
	// design exists to eliminate. Attach() is now the ONLY place that calls
	// SetWindowPos with a size.
	hwnd := win.HWND(w.Window())
	win.ShowWindow(hwnd, win.SW_HIDE)
	canvasGlobal.Attach()
	win.ShowWindow(hwnd, win.SW_SHOWNOACTIVATE)

	w.Bind("uiReady", func() {
		log.Printf("[ui] uiReady — JS finished loading")
		bridge.Ready()
	})
	w.Bind("getCanvasSize", func() map[string]float64 {
		return map[string]float64{"w": canvasCSSWidth, "h": canvasCSSHeight}
	})
	w.Bind("setRegionRects", func(rects []Rect) {
		w.Dispatch(func() { canvasGlobal.SetRects(rects) })
	})

	assets, err := startAssetServer()
	if err != nil {
		log.Fatalf("[ui] cannot start asset server: %v", err)
	}
	defer assets.Close()
	log.Printf("[ui] assets at %s", assets.URL)
	w.Navigate(assets.URL + "index.html")

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
