package ui

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
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
		return
	}
	sw := win.GetSystemMetrics(win.SM_CXSCREEN)
	x := (int(sw) - width) / 2

	// 1. Physically resize and reposition the window
	win.SetWindowPos(hwndGlobal, win.HWND_TOPMOST, int32(x), 10, int32(width), int32(height), win.SWP_NOACTIVATE)

	// 2. Create a rounded rectangle region and APPLY it to the window
	hrgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(width), uintptr(height), uintptr(radius*2), uintptr(radius*2))
	procSetWindowRgn.Call(uintptr(hwndGlobal), hrgn, 1)
}

func StartOverlay(ctx context.Context, cfg *config.Config) {
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
	w.Bind("callResize", func(width, height, radius int) {
		w.Dispatch(func() { resizeWindow(width, height, radius) })
	})
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

			resizeWindow(defaultW, defaultH, 22)
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
