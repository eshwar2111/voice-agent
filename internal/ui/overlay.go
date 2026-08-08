package ui

import (
	"context"
	"encoding/json"
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
	// confirmMutex serializes RequestConfirmation/RequestConfirmationCard.
	// Both share one confirmChan and one 'trust.approval' activity slot; the
	// typed `ai …` path (OnCommand, run via `go` in submitCommand's bind, see
	// StartOverlay below) isn't covered by the engine's isBusy guard, so it
	// can overlap the voice path. Without this, two goroutines could both
	// block on <-confirmChan while the second UpdateActivity silently
	// overwrote the first's prompt — one click would answer one goroutine and
	// the other would hang forever, with no UI and no log (spec §7: "never
	// deadlocks the executor"). Serializing means the second caller simply
	// waits its turn for the island instead.
	confirmMutex sync.Mutex

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
	UpdateActivity("ambient.nudge", map[string]any{
		"id":      "meeting",
		"icon":    "calendar",
		"title":   title,
		"message": text,
		"action":  "",
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

// canDeliverConfirmation reports whether an approve prompt can actually reach
// the WebView. It is pure (no globals) so it can be unit tested directly: the
// real bug this guards was a real window (w != nil) with no bridge yet
// (bridge == nil, the window between the two assignments in StartOverlay),
// which used to let RequestConfirmation(Card) push into a nil Bridge and then
// block forever on <-confirmChan — a hung plan step with no error and no UI.
func canDeliverConfirmation(hasWindow, hasBridge bool) bool {
	return hasWindow && hasBridge
}

// approvalCardFields best-effort extracts a headline and a goal/body string
// out of the plan-card JSON built by callers (agent.executor, security).
// It never fails the request on a parse miss — worst case the raw JSON shows
// up as the goal text, which is still readable, just unformatted.
func approvalCardFields(cardJSON string) (title, goal string) {
	title, goal = "Approve action?", cardJSON
	var p struct {
		Title string `json:"title"`
		Plan  struct {
			Goal string `json:"goal"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(cardJSON), &p); err == nil {
		if p.Title != "" {
			title = p.Title
		}
		if p.Plan.Goal != "" {
			goal = p.Plan.Goal
		}
	}
	return title, goal
}

// RequestConfirmationCard and RequestConfirmation both drive the
// trust.approval live activity so the island expands inline with Approve/
// Cancel, rather than opening a full panel. trust.approval has no ttl (see
// activities.js): it resolves only when resolveConfirm(bool) fires from the
// island's own buttons, never by timeout.
func RequestConfirmationCard(cardJSON string) bool {
	if !canDeliverConfirmation(w != nil, bridge != nil) {
		log.Printf("[ui] confirmation requested before bridge ready — denying")
		return false
	}
	waitForConfirmSlot()
	defer confirmMutex.Unlock()
	title, goal := approvalCardFields(cardJSON)
	UpdateActivity("trust.approval", map[string]any{"title": title, "goal": goal})
	return <-confirmChan
}

func RequestConfirmation(msg string) bool {
	if !canDeliverConfirmation(w != nil, bridge != nil) {
		log.Printf("[ui] confirmation requested before bridge ready — denying")
		return false
	}
	waitForConfirmSlot()
	defer confirmMutex.Unlock()
	UpdateActivity("trust.approval", map[string]any{"title": "Approve action?", "goal": msg})
	return <-confirmChan
}

// waitForConfirmSlot acquires confirmMutex, logging if the caller actually
// had to wait — i.e. another plan step's approval was already showing. Silent
// contention here is exactly what made this bug unfindable before: two
// concurrent callers (typed `ai …` isn't covered by the engine's isBusy
// guard, so it can overlap the voice path) would otherwise both reach
// UpdateActivity, the second silently clobbering the first's prompt.
func waitForConfirmSlot() {
	if confirmMutex.TryLock() {
		return
	}
	log.Printf("[ui] confirmation request queued behind a pending approval")
	confirmMutex.Lock()
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

	// Fix-round-3 (R3) defense-in-depth: main.js's resolveConfirm now refuses
	// to call this at all for a stray second click (isLive('trust.approval')
	// false and no approve sheet open), so in normal operation this should
	// never see an extra call. But confirmChan is unbuffered and shared
	// across every serialized RequestConfirmation(Card) call (confirmMutex,
	// fix-round-2 I2) — if an unexpected extra send ever DID happen here, a
	// blocking send could silently land on the NEXT queued caller's receive
	// instead of the one it was meant for, auto-answering a prompt the user
	// never saw. A non-blocking send drops (and logs) anything that arrives
	// with no receiver already waiting, rather than risk that delivery.
	// Fail-closed: a dropped send never becomes an approval either way — the
	// legitimate caller it was meant for just keeps waiting, unaffected.
	w.Bind("confirmCallback", func(approved bool) {
		select {
		case confirmChan <- approved:
		default:
			log.Printf("[ui] confirmCallback(%v) had no pending confirmation to answer — dropped", approved)
		}
	})
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
		// Flush any provider-driven activity snapshot that arrived before bridge
		// existed (island providers can start publishing before this point — see
		// FlushPendingActivities). Must run after Ready() so it drains through the
		// same buffered path as everything queued before uiReady.
		FlushPendingActivities()
	})
	w.Bind("getCanvasSize", func() map[string]float64 {
		return map[string]float64{"w": canvasCSSWidth, "h": canvasCSSHeight}
	})
	w.Bind("setRegionRects", func(rects []Rect) {
		w.Dispatch(func() { canvasGlobal.SetRects(rects) })
	})
	w.Bind("dismissIslandActivity", func(id string) {
		if islandRegistry != nil {
			islandRegistry.Dismiss(id)
		}
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
