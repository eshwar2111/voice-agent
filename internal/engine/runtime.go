package engine

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/agent"
	"github.com/yourname/voice-agent/internal/asr"
	"github.com/yourname/voice-agent/internal/audio"
	"github.com/yourname/voice-agent/internal/audit"
	agentctx "github.com/yourname/voice-agent/internal/context"
	"github.com/yourname/voice-agent/internal/dispatch"
	"github.com/yourname/voice-agent/internal/executor"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/memory"
	"github.com/yourname/voice-agent/internal/resolver"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
	"github.com/yourname/voice-agent/internal/trust"
	"github.com/yourname/voice-agent/internal/ui"
)

type EventType string

const (
	EventStart        EventType = "START"
	EventVoiceInput   EventType = "VOICE_INPUT"
	EventTranscribed  EventType = "TRANSCRIBED"
	EventPlanReady    EventType = "PLAN_READY"
	EventToolExecuted EventType = "TOOL_EXECUTED"
	EventError        EventType = "ERROR"
)

type Event struct {
	Type    EventType
	Payload interface{}
	Err     error
}

type transcribedPayload struct {
	Transcript string
	Cap        agentctx.Capture
}

type Engine struct {
	Config       *config.Config
	Provider     llm.Provider
	Registry     *tools.Registry
	MemStore     *memory.Store
	MemRetriever *memory.Retriever
	RateLimiter  *security.RateLimiter
	Profile      *security.Profile
	Dispatch     *dispatch.Deps

	History  []string
	Events   chan Event
	isBusy   bool // true while processing a command pipeline
	busyLock sync.Mutex
	// pending is the trigger that started the in-flight command, held so its
	// waiter is released by that command's completion and nothing else.
	pending ui.Trigger
	// curCancel cancels the context of the command currently being dispatched.
	// Ctrl+Esc (the kill switch) calls it via CancelCurrent to halt just the
	// in-flight command, leaving the engine, ambient loop, island, and WebView
	// alive. It is nil between commands; both it and pending are guarded by
	// busyLock.
	curCancel context.CancelFunc
}

func NewEngine(cfg *config.Config, provider llm.Provider, registry *tools.Registry, store *memory.Store, retriever *memory.Retriever, rateLimiter *security.RateLimiter, profile *security.Profile, trusted *trust.TrustedExecutor) *Engine {
	return &Engine{
		Config:       cfg,
		Provider:     provider,
		Registry:     registry,
		MemStore:     store,
		MemRetriever: retriever,
		RateLimiter:  rateLimiter,
		Profile:      profile,
		Dispatch: &dispatch.Deps{
			Registry: registry,
			Provider: provider,
			Profile:  profile,
			Resolver: resolver.Default(),
			Trusted:  trusted,
		},
		Events: make(chan Event, 100),
	}
}

// Start initiates the engine runtime
func (e *Engine) Start(ctx context.Context) {
	// Bridge UI trigger to Event bus
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ui.ListenTrigger:
				e.emit(ctx, Event{Type: EventVoiceInput, Payload: t})
			}
		}
	}()

	ui.SetState(ui.StateIdle)
	log.Println("Engine runtime started and waiting for events...")

	for {
		select {
		case <-ctx.Done():
			log.Println("Engine shutting down gracefully...")
			return
		case ev := <-e.Events:
			e.handleEvent(ctx, ev)
		}
	}
}

func (e *Engine) handleEvent(ctx context.Context, ev Event) {
	switch ev.Type {
	case EventVoiceInput:
		// A trigger carries its own completion channel. Releasing THIS trigger on
		// rejection is correct — it did no work, so its waiter should stop waiting
		// immediately — and, unlike the old shared signal, it cannot be mistaken
		// for the completion of the command that is actually running.
		trig, _ := ev.Payload.(ui.Trigger)

		if executor.IsSpeaking() {
			fmt.Println("⚠️  TTS is active — ignoring trigger to prevent feedback loop.")
			trig.Finish()
			return
		}

		e.busyLock.Lock()
		if e.isBusy {
			e.busyLock.Unlock()
			fmt.Println("⚠️  Already processing a command — ignoring trigger.")
			trig.Finish()
			return
		}
		e.isBusy = true
		// Hold this trigger's completion channel for the life of the command, so
		// EventToolExecuted/EventError release the trigger that started the work
		// rather than whichever waiter happened to be listening.
		e.pending = trig
		e.busyLock.Unlock()

		ui.SetState(ui.StateListening)

		go func() {
			cap := agentctx.CaptureAmbient(true) // app still focused (pill never steals focus)
			audioData, err := audio.RecordDynamic(10*time.Second, 0.01, 32000)
			if err != nil {
				e.emit(ctx, Event{Type: EventError, Err: fmt.Errorf("record missing: %w", err)})
				return
			}

			if len(audioData) < 1600 {
				e.emit(ctx, Event{Type: EventError, Err: fmt.Errorf("audio too short")})
				return
			}

			ui.SetState(ui.StateReasoning)
			fmt.Println("🎧 Transcribing audio with in-process Whisper...")
			transcript, err := asr.Transcribe(audioData)
			if err != nil {
				e.emit(ctx, Event{Type: EventError, Err: fmt.Errorf("transcription failed: %w", err)})
				return
			}
			e.emit(ctx, Event{Type: EventTranscribed, Payload: transcribedPayload{Transcript: transcript, Cap: cap}})
		}()

	case EventTranscribed:
		p := ev.Payload.(transcribedPayload)
		fmt.Printf("📝 Transcript: %s\n", p.Transcript)

		// Route voice transcripts through the tiered dispatcher (Tier 0 local
		// resolver first, falling back to Tier 1 cloud orchestration).
		go func() {
			ui.SetState(ui.StateExecuting)

			// Bound the whole dispatch. Without a deadline the ctx here is the
			// root app context, cancelled only at shutdown — so any call that
			// never returns (the classic case being a cloud LLM request on a
			// half-dead network) leaves this goroutine parked forever. It never
			// emits, so isBusy is never cleared, and from then on EVERY trigger
			// hits the "already processing" branch: the island sticks on
			// Executing and the agent is dead until the process is restarted.
			// The transport-level deadlines in internal/llm make that specific
			// hang unreachable; this is the guarantee that no future blocking
			// call anywhere under Handle can resurrect the same wedge.
			dctx, cancel := context.WithTimeout(ctx, dispatchDeadline)
			defer cancel()
			// Publish this command's cancel so Ctrl+Esc can halt just this
			// command (see CancelCurrent). Cleared when the command finishes so
			// a later kill-switch press is a no-op rather than cancelling a
			// stale context.
			e.setCurrentCancel(cancel)
			defer e.clearCurrentCancel()

			if err := e.Dispatch.Handle(dctx, p.Transcript, p.Cap); err != nil {
				e.emit(ctx, Event{Type: EventError, Err: fmt.Errorf("dispatch failed: %w", err)})
				audit.LogAction(p.Transcript, "dispatch", nil, "FAILED: "+err.Error())
				return
			}
			audit.LogAction(p.Transcript, "dispatch", nil, "SUCCESS")
			e.emit(ctx, Event{Type: EventToolExecuted, Payload: agent.Plan{Transcript: p.Transcript, Intent: "dispatch"}})
		}()

	case EventToolExecuted:
		if plan, ok := ev.Payload.(agent.Plan); ok {
			summary := fmt.Sprintf("User: %s | Intent: %s", plan.Transcript, plan.Intent)
			e.History = append(e.History, summary)
			if len(e.History) > 5 {
				e.History = e.History[len(e.History)-5:]
			}
		}

		fmt.Println("✅ Done execution.")
		if !executor.IsSpeaking() {
			ui.SetState(ui.StateIdle)
		}
		e.finishCommand()

	case EventError:
		log.Printf("Engine Error Event: %v", ev.Err)
		ui.SetState(ui.StateIdle)
		e.finishCommand()
	}
}

// dispatchDeadline is the absolute ceiling on one command's execution. Set well
// above any legitimate multi-step plan (research fan-outs, GUI automation with
// waits) — it exists to guarantee the busy flag always clears, not to police
// slow work.
const dispatchDeadline = 5 * time.Minute

// emit posts an event unless the engine is already shutting down.
//
// Start()'s loop stops draining e.Events the moment ctx is cancelled, so a bare
// `e.Events <- ...` from a background goroutine parks forever once the buffer
// fills — one leaked goroutine per shutdown-during-command, holding whatever it
// captured. Selecting on ctx.Done() makes the send give up instead.
func (e *Engine) emit(ctx context.Context, ev Event) {
	select {
	case e.Events <- ev:
	case <-ctx.Done():
	}
}

// IsBusy reports whether the engine is currently processing a command pipeline
// (pill- or wake-word-initiated). Used by the wake-word loop to pause its own
// recorder while another capture is in flight.
func (e *Engine) IsBusy() bool {
	e.busyLock.Lock()
	defer e.busyLock.Unlock()
	return e.isBusy
}

// finishCommand clears the busy flag and releases the trigger that started the
// command. Clearing e.pending as it goes makes a double release impossible,
// which is what lets the release be a channel close (and so wake every waiter)
// instead of a single-slot send.
func (e *Engine) finishCommand() {
	e.busyLock.Lock()
	e.isBusy = false
	trig := e.pending
	e.pending = ui.Trigger{}
	e.busyLock.Unlock()
	trig.Finish()
}

// setCurrentCancel records the cancel func of the command being dispatched.
func (e *Engine) setCurrentCancel(cancel context.CancelFunc) {
	e.busyLock.Lock()
	e.curCancel = cancel
	e.busyLock.Unlock()
}

// clearCurrentCancel drops the reference once the command has finished, so a
// later CancelCurrent does not fire against a stale context.
func (e *Engine) clearCurrentCancel() {
	e.busyLock.Lock()
	e.curCancel = nil
	e.busyLock.Unlock()
}

// CancelCurrent halts the command currently in flight by cancelling its context.
// This is the Ctrl+Esc kill switch: it stops the active command only and leaves
// the root context — and therefore the engine, ambient loop, island, and WebView
// — untouched. It is a no-op when nothing is running.
func (e *Engine) CancelCurrent() {
	e.busyLock.Lock()
	cancel := e.curCancel
	e.busyLock.Unlock()
	if cancel != nil {
		log.Println("Engine: cancelling current command (kill switch)")
		cancel()
	}
}

// TriggerAndWait fires a voice capture (as if the pill were clicked) and blocks until the
// command finishes or timeout elapses. Used by the wake-word loop to hand the mic back only
// after the command is done.
//
// The done channel is created here and travels WITH the trigger, so this waiter
// can only ever be released by its own trigger — either because the command it
// started finished, or because the engine rejected it outright. There is no
// longer a shared signal for an unrelated command to trip.
func (e *Engine) TriggerAndWait(timeout time.Duration) {
	done := make(chan struct{})
	select {
	case ui.ListenTrigger <- ui.Trigger{Done: done}:
	case <-time.After(2 * time.Second):
		return // engine not consuming triggers; give up
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
