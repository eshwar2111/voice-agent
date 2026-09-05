package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lxn/win"
	webview "github.com/webview/webview_go"
	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"github.com/yourname/voice-agent/internal/island"
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

// Trigger is a request to start a voice capture.
//
// Done exists so a caller can wait for THIS trigger specifically. The engine
// previously acknowledged completion on one shared channel with no link to the
// trigger being acknowledged, so a duplicate trigger the engine had rejected
// could satisfy an unrelated capture that was still in flight — the wake-word
// loop would reclaim the microphone mid-command and re-fire. Carrying the
// completion channel with the trigger makes that mix-up impossible.
//
// A nil Done means fire-and-forget (the pill click); the engine simply has
// nothing to close.
type Trigger struct {
	Done chan struct{}

	// FollowUp marks a capture the agent re-armed on its own right after
	// speaking, so "actually, make it tomorrow" works without a wake word or a
	// click. It uses a shorter no-speech window and, hearing nothing, returns to
	// idle silently instead of surfacing an "audio too short" error.
	FollowUp bool
}

// Finish closes t.Done if there is one. Safe to call on a zero Trigger.
func (t Trigger) Finish() {
	if t.Done != nil {
		close(t.Done)
	}
}

var (
	currentState  AgentState = StateBoot
	stateMutex    sync.Mutex
	w             webview.WebView
	hwndGlobal    win.HWND
	canvasGlobal  *canvas
	bridge        *Bridge
	ListenTrigger = make(chan Trigger)

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

	// askTextChan carries a free-text clarification answer back from the UI to a
	// blocked AskText call; askTextMutex serializes concurrent clarifications so
	// they can't share the one channel (same reasoning as confirmMutex).
	askTextChan  chan askAnswer
	askTextMutex sync.Mutex

	// askChoiceChan carries the selected option id back from the UI to a blocked
	// AskChoice call; askChoiceMutex serializes concurrent choice prompts on the
	// one channel (same reasoning as askTextMutex/confirmMutex).
	askChoiceChan  chan choiceAnswer
	askChoiceMutex sync.Mutex

	// OnCommand is declared in command_bar.go
	OnSettingsSaved func(cfg interface{})
)

// askAnswer is the result of a clarification prompt: the typed text and whether
// the user submitted (ok) rather than cancelled.
type askAnswer struct {
	text string
	ok   bool
}

// Option is one selectable choice on the interactive choice surface. Label is
// the visible line; Sub is an optional secondary/meta line; ID is what AskChoice
// returns when this option is picked. The json tags are the field names the
// askchoice.js surface reads off the pushed payload.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Sub   string `json:"sub"`
}

// choiceAnswer is the result of a choice prompt: the selected option id and
// whether the user actually chose (ok) rather than cancelled.
type choiceAnswer struct {
	id string
	ok bool
}

func init() {
	// Force WebView2 background to be transparent (0 alpha)
	// This must be set before the webview is initialized.
	os.Setenv("WEBVIEW2_DEFAULT_BACKGROUND_COLOR", "0")
	confirmChan = make(chan bool)
	askTextChan = make(chan askAnswer)
	askChoiceChan = make(chan choiceAnswer)
}

// SpeakFunc, when non-nil, speaks an answer aloud. It is injected once from
// main (wired to executor.Speak) and gated by cfg.SpeakResponses — left nil the
// UI stays silent. Keeping it as an injected func means internal/ui takes no
// dependency on internal/executor, preserving the layering.
var SpeakFunc func(string)

// StopSpeakFunc, when non-nil, halts any in-progress speech (wired to
// executor.StopSpeaking). Invoked by the island's Stop control while speaking.
var StopSpeakFunc func()

// ReloadProvider, when set (by main), rebuilds the LLM provider from the updated
// config and hot-swaps it into the running app after Settings are saved — so a
// provider/key/model/fallback change from the Models tab applies immediately.
var ReloadProvider func()

// speakMode is toggled per command by the engine: on for a voice-originated
// command, off for the typed path. Only ShowOutputOverlay (answers) consults it;
// status messages via ShowNotification are never spoken.
//
// Invariant: correctness depends on voice and typed dispatch never overlapping,
// which the engine's single-flight isBusy guard currently ensures. If a future
// change lets a typed command's ShowOutputOverlay run while a voice command still
// holds speakMode=true, the typed answer would be spoken — make speakMode
// per-command (not a process global) before allowing concurrent dispatch.
var speakMode atomic.Bool

// SetSpeakMode enables or disables speaking of answers for the current command.
func SetSpeakMode(on bool) {
	speakMode.Store(on)
}

var (
	gistURLRe    = regexp.MustCompile(`https?://\S+`)
	gistMDRe     = regexp.MustCompile("[#*_`>|-]+") // markdown syntax noise
	gistWSRe     = regexp.MustCompile(`\s+`)
	gistSentEnd  = regexp.MustCompile(`[.!?]`)
	gistMaxRunes = 320
)

// spokenGist reduces an answer to a short, natural spoken summary: strip URLs
// and markdown, collapse whitespace, and keep the first sentence or two (up to
// ~320 chars). The screen still shows the full text; only the voice is trimmed.
func spokenGist(text string) string {
	s := gistURLRe.ReplaceAllString(text, "")
	s = strings.ReplaceAll(s, "```", " ")
	s = gistMDRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(gistWSRe.ReplaceAllString(s, " "))
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= gistMaxRunes {
		return s
	}
	// Cut at a sentence boundary near the cap, else a hard cut with an ellipsis.
	head := string(runes[:gistMaxRunes])
	if loc := gistSentEnd.FindAllStringIndex(head, -1); len(loc) > 0 {
		last := loc[len(loc)-1][1]
		if last > gistMaxRunes/3 {
			return strings.TrimSpace(head[:last])
		}
	}
	return strings.TrimSpace(head) + "…"
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

// SetAgentProgress drives the agent.run live activity with REAL, structured
// step data. The orchestrator calls it as it advances sub-goals — it already
// knows the current index and the total — so the island can render a
// determinate progress ring plus an "N of M · label" line instead of an
// opaque status string.
//
// It rides the same activity:update channel the state/notify handlers use, so
// on the JS side it REPLACES the agent.run entry's data with {phase, step,
// total, label}. When no progress has been published (single-step commands, or
// any tick where a state/notify update overwrote it), agent.run falls back to
// its SP5 indeterminate glyph/shimmer: the renderer keys the ring on step/total
// being present, so their absence is a clean fallback, never a broken 0% ring.
func SetAgentProgress(step, total int, label string) {
	UpdateActivity("agent.run", map[string]any{
		"phase": "acting",
		"step":  step,
		"total": total,
		"label": label,
	})
}

func ShowCommandBarInOverlay() {
	if w == nil {
		return
	}
	bridge.Push("surface:open", map[string]string{"id": "command"})
}

// lastLevelPush throttles mic-level pushes to the WebView (the audio callback
// fires ~10/s, which is already fine, but this guards against a chattier device
// and against pushing before the bridge exists).
var lastLevelPush atomic.Int64

// PushMicLevel forwards a captured-frame RMS to the island so the listening
// waveform reacts to the user's actual voice. Called from the audio capture
// thread, so it stays cheap and drops frames rather than blocking. RMS for
// speech sits around 0.05–0.25; it is scaled and clamped to 0..1 for the UI.
func PushMicLevel(rms float64) {
	if bridge == nil {
		return
	}
	now := time.Now().UnixMilli()
	last := lastLevelPush.Load()
	if now-last < 55 {
		return
	}
	if !lastLevelPush.CompareAndSwap(last, now) {
		return
	}
	lvl := rms * 4.0
	if lvl > 1 {
		lvl = 1
	} else if lvl < 0 {
		lvl = 0
	}
	bridge.Push("mic:level", map[string]any{"level": lvl})
}

func ShowOutputOverlay(text string) {
	if w == nil {
		return
	}
	// Speak the answer for voice-originated commands. This is the single point
	// every textual ANSWER funnels through (short answers still route through
	// ShowNotification below, but only from here — direct ShowNotification/
	// SetState status messages never reach this call and so are never spoken).
	// Run off the UI dispatch: SpeakFunc blocks on SAPI, and this may be called
	// on the WebView thread. speakMode is off for the typed path. Speak only the
	// GIST — the screen can show the full answer (lists, links, detail), but
	// reading all of that aloud is painful; voice gets a concise spoken summary.
	if speakMode.Load() && SpeakFunc != nil {
		if gist := spokenGist(text); gist != "" {
			go SpeakFunc(gist)
		}
	}
	if len(text) < 55 {
		ShowNotification(text)
		return
	}
	bridge.Push("surface:open", map[string]any{"id": "result", "text": text})
}

// BeginStreamedAnswer opens the result surface empty, ready to receive deltas
// from a streaming answer so text appears as it is generated.
func BeginStreamedAnswer() {
	if bridge == nil {
		return
	}
	bridge.Push("surface:open", map[string]any{"id": "result", "text": "", "streaming": true})
}

// PushAnswerDelta appends one chunk of a streaming answer to the open result
// surface (rendered as progressive plain text; the final render is authoritative).
func PushAnswerDelta(chunk string) {
	if bridge == nil || chunk == "" {
		return
	}
	bridge.Push("result:delta", map[string]any{"text": chunk})
}

// EndStreamedAnswer finalizes a streamed answer. It re-renders the FULL text via
// the same known-good path ShowOutputOverlay uses (so any streaming glitch is
// corrected to the authoritative content), and speaks the gist for voice
// commands. Safe even if no deltas were delivered.
func EndStreamedAnswer(full string) {
	if speakMode.Load() && SpeakFunc != nil {
		if gist := spokenGist(full); gist != "" {
			go SpeakFunc(gist)
		}
	}
	if bridge == nil {
		return
	}
	bridge.Push("surface:open", map[string]any{"id": "result", "text": full})
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

// approvalCardFields extracts a readable headline, a one-line goal, and the list
// of steps being approved out of the plan-card JSON (agent.executor / security /
// trust preview). It NEVER dumps the raw JSON as the body — an unparseable card
// yields an empty goal and no steps, and the card still shows a clear title.
func approvalCardFields(cardJSON string) (title, goal string, steps []map[string]string) {
	title = "Approve action?"
	var p struct {
		Title string `json:"title"`
		Plan  struct {
			Goal  string `json:"goal"`
			Steps []struct {
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"steps"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(cardJSON), &p); err == nil {
		if p.Title != "" {
			title = p.Title
		}
		goal = p.Plan.Goal
		for _, s := range p.Plan.Steps {
			steps = append(steps, map[string]string{"label": s.Label, "value": s.Value})
		}
	}
	return title, goal, steps
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
	title, goal, steps := approvalCardFields(cardJSON)
	UpdateActivity("trust.approval", map[string]any{"title": title, "goal": goal, "steps": steps})
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

// AskText opens a free-text clarification prompt on the island and blocks until
// the user answers (returns text, true) or cancels / the UI is unavailable
// (returns "", false). Used when a tool needs a value the request didn't
// specify — the agent asks instead of failing. Serialized so two clarifications
// can't race on the one channel.
func AskText(question string) (string, bool) {
	if w == nil || bridge == nil {
		return "", false
	}
	askTextMutex.Lock()
	defer askTextMutex.Unlock()
	bridge.Push("surface:open", map[string]any{"id": "ask", "question": question})
	ans := <-askTextChan
	return ans.text, ans.ok
}

// AskChoice opens the interactive choice surface on the island — a question and
// a vertical list of selectable options — and blocks until the user picks one
// (returns the chosen option's ID, true) or cancels / the UI is unavailable
// (returns "", false). It is the structured-choice counterpart to AskText: use
// it when the agent needs the user to CHOOSE among known alternatives (a
// disambiguation, a clarification with fixed answers, an approve-with-options)
// rather than type free text. The options are answerable by click, keyboard, or
// — routing a spoken answer through the same askChoiceSubmit binding — voice.
// Serialized so two choice prompts can't race on the one channel.
func AskChoice(question string, options []Option) (string, bool) {
	if w == nil || bridge == nil {
		return "", false
	}
	askChoiceMutex.Lock()
	defer askChoiceMutex.Unlock()
	bridge.Push("surface:open", map[string]any{"id": "askchoice", "question": question, "options": options})
	ans := <-askChoiceChan
	return ans.id, ans.ok
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

	// Fire-and-forget: a pill click has nobody waiting on the outcome, so Done
	// is nil. Only TriggerAndWait (the wake-word path) supplies one.
	w.Bind("triggerListen", func() { ListenTrigger <- Trigger{} })
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
	// Clarification prompt answers. Non-blocking send (like confirmCallback):
	// if no AskText is waiting, drop it rather than block the UI thread or land
	// on an unrelated future receiver.
	w.Bind("askTextSubmit", func(text string) {
		select {
		case askTextChan <- askAnswer{text: text, ok: true}:
		default:
			log.Printf("[ui] askTextSubmit had no pending clarification — dropped")
		}
	})
	w.Bind("askTextCancel", func() {
		select {
		case askTextChan <- askAnswer{ok: false}:
		default:
		}
	})
	// Choice-surface answers. Same non-blocking contract as askTextSubmit/Cancel:
	// deliver to a waiting AskChoice, otherwise drop (a submit followed by the
	// surface's own close-cancel, or a stray click with nothing pending).
	w.Bind("askChoiceSubmit", func(id string) {
		select {
		case askChoiceChan <- choiceAnswer{id: id, ok: true}:
		default:
			log.Printf("[ui] askChoiceSubmit had no pending choice — dropped")
		}
	})
	w.Bind("askChoiceCancel", func() {
		select {
		case askChoiceChan <- choiceAnswer{ok: false}:
		default:
		}
	})
	// Stop control shown while the agent is speaking — halts TTS immediately.
	w.Bind("taskStop", func() { invokeProgressCancel() })
	w.Bind("stopSpeaking", func() {
		if StopSpeakFunc != nil {
			StopSpeakFunc()
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
			"llm_provider":      cfg.LLMProvider,
			"api_key":           cfg.APIKey,
			"model":             cfg.Model,
			"base_url":          cfg.BaseURL,
			"fallback_provider": cfg.FallbackProvider,
			"fallback_api_key":  cfg.FallbackAPIKey,
			"fallback_model":    cfg.FallbackModel,
			"enable_voice":      cfg.EnableVoice,
			"privacy_mode":      cfg.PrivacyMode,
		}
	})
	w.Bind("saveSettings", func(provider, apiKey, model, baseURL, fbProvider, fbAPIKey, fbModel string, enableVoice, privacyMode bool) bool {
		if provider != "" {
			cfg.LLMProvider = provider
		}
		cfg.APIKey = apiKey
		cfg.Model = model
		cfg.BaseURL = baseURL
		cfg.FallbackProvider = fbProvider
		cfg.FallbackAPIKey = fbAPIKey
		cfg.FallbackModel = fbModel
		cfg.EnableVoice = enableVoice
		cfg.PrivacyMode = privacyMode
		if err := config.SaveConfig("config.json", cfg); err != nil {
			fmt.Printf("Save settings error: %v\n", err)
			return false
		}
		// Hot-swap the live provider so the change takes effect without a restart.
		if ReloadProvider != nil {
			ReloadProvider()
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
	// webview_go handles WM_DPICHANGED itself and resizes the window without
	// telling us, which silently breaks the never-resize invariant when the app
	// moves to a monitor with different scaling. Reconcile instead of fighting
	// it for ownership of the message loop.
	canvasGlobal.watchGeometry(ctx)

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
	// Timer controls from the timer.expanded activity (activities.js). The JS
	// side passes the ACTIVITY id ("timer.<key>"); the store is keyed on the
	// bare <key>, so strip the "timer." prefix before hitting DefaultTimers —
	// the same store the island runner supervises (cmd/app/main.go). Pause
	// freezes remaining time; Resume restarts from it; Cancel removes the timer
	// outright (the spec's Cancel == Remove). All three are no-ops on the store
	// for an unknown id, so a stale click can never panic.
	w.Bind("timerPause", func(id string) {
		island.DefaultTimers.Pause(strings.TrimPrefix(id, "timer."))
	})
	w.Bind("timerResume", func(id string) {
		island.DefaultTimers.Resume(strings.TrimPrefix(id, "timer."))
	})
	w.Bind("timerCancel", func(id string) {
		island.DefaultTimers.Remove(strings.TrimPrefix(id, "timer."))
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
			// The status cache must not outlive the thing it describes: without
			// this the badge would keep reporting the pre-link state for up to
			// the TTL, so a successful connect looks like it failed.
			invalidateAuthStatus("google")
			if err != nil {
				fmt.Printf("Google Auth Error: %v\n", err)
			} else {
				// Refresh integration status in UI after successful link
				w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
			}
		}()
	})

	w.Bind("unlinkGoogle", func() {
		invalidateAuthStatus("google")
		cfg.GoogleToken = ""
		config.SaveConfig("config.json", cfg)
		w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
	})

	w.Bind("getGoogleStatus", func() map[string]interface{} {
		return cachedAuthStatus("google", func() map[string]interface{} {
			store := auth.NewTokenStore("config.json")
			if _, err := store.LoadToken(auth.ProviderGoogle, cfg); err != nil {
				return map[string]interface{}{"connected": false}
			}
			// A STORED token is not a WORKING token. LoadToken only reads what is in
			// config.json, so a refresh token revoked by Google — expired, password
			// changed, consent withdrawn — still reported "Connected" while every
			// actual call failed with invalid_grant. The user saw a healthy badge
			// and no reason to re-link, while the calendar provider retried into a
			// backoff ladder forever. Exchange it for real: that is the only thing
			// that distinguishes a live link from a dead one.
			if _, err := auth.GetGoogleClient(ctx, cfg); err != nil {
				log.Printf("[ui] Google token present but not usable: %v", err)
				return map[string]interface{}{
					"connected": false,
					"expired":   true,
					"reason":    "Sign-in expired — reconnect to restore Gmail, Calendar and Drive.",
				}
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
			// The status cache must not outlive the thing it describes: without
			// this the badge would keep reporting the pre-link state for up to
			// the TTL, so a successful connect looks like it failed.
			invalidateAuthStatus("spotify")
			if err != nil {
				fmt.Printf("Spotify Auth Error: %v\n", err)
			} else {
				w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
			}
		}()
	})

	w.Bind("unlinkSpotify", func() {
		invalidateAuthStatus("spotify")
		cfg.SpotifyToken = ""
		config.SaveConfig("config.json", cfg)
		w.Dispatch(func() { w.Eval("loadIntegrationStatusesDash();") })
	})

	w.Bind("getSpotifyStatus", func() map[string]interface{} {
		return cachedAuthStatus("spotify", func() map[string]interface{} {
			store := auth.NewTokenStore("config.json")
			if _, err := store.LoadToken(auth.ProviderSpotify, cfg); err != nil {
				return map[string]interface{}{"connected": false}
			}
			// Same trap as Google: a stored token is not a working one. The user
			// info call is the cheapest way to actually exercise it, and it is
			// already being made — it just used to be treated as optional garnish
			// while "connected" was hardcoded true.
			info, err := auth.GetSpotifyUserInfo(ctx, cfg)
			if err != nil {
				log.Printf("[ui] Spotify token present but not usable: %v", err)
				return map[string]interface{}{
					"connected": false,
					"expired":   true,
					"reason":    "Spotify sign-in expired or unreachable — reconnect to restore playback control.",
				}
			}
			res := map[string]interface{}{
				"connected":    true,
				"capabilities": []string{"Playback", "Queue", "Recommendations", "AI Curation"},
			}
			if info != nil {
				res["display_name"] = info.DisplayName
				res["product"] = info.Product
			}
			return res
		})
	})
}
