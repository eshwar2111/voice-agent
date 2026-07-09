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

	History     []string
	Events      chan Event
	isBusy      bool // true while processing a command pipeline
	busyLock    sync.Mutex
	commandDone chan struct{}
}

func NewEngine(cfg *config.Config, provider llm.Provider, registry *tools.Registry, store *memory.Store, retriever *memory.Retriever, rateLimiter *security.RateLimiter, profile *security.Profile) *Engine {
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
		},
		Events:      make(chan Event, 100),
		commandDone: make(chan struct{}, 1),
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
			case <-ui.ListenTrigger:
				e.Events <- Event{Type: EventVoiceInput}
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
		if executor.IsSpeaking() {
			fmt.Println("⚠️  TTS is active — ignoring trigger to prevent feedback loop.")
			e.signalCommandDone()
			return
		}

		e.busyLock.Lock()
		if e.isBusy {
			e.busyLock.Unlock()
			fmt.Println("⚠️  Already processing a command — ignoring trigger.")
			e.signalCommandDone()
			return
		}
		e.isBusy = true
		e.busyLock.Unlock()

		ui.SetState(ui.StateListening)

		go func() {
			cap := agentctx.CaptureAmbient(true) // app still focused (pill never steals focus)
			audioData, err := audio.RecordDynamic(10*time.Second, 0.01, 32000)
			if err != nil {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("record missing: %w", err)}
				return
			}

			if len(audioData) < 1600 {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("audio too short")}
				return
			}

			ui.SetState(ui.StateReasoning)
			fmt.Println("🎧 Transcribing audio with in-process Whisper...")
			transcript, err := asr.Transcribe(audioData)
			if err != nil {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("transcription failed: %w", err)}
				return
			}
			e.Events <- Event{Type: EventTranscribed, Payload: transcribedPayload{Transcript: transcript, Cap: cap}}
		}()

	case EventTranscribed:
		p := ev.Payload.(transcribedPayload)
		fmt.Printf("📝 Transcript: %s\n", p.Transcript)

		// Route voice transcripts through the tiered dispatcher (Tier 0 local
		// resolver first, falling back to Tier 1 cloud orchestration).
		go func() {
			ui.SetState(ui.StateExecuting)
			if err := e.Dispatch.Handle(ctx, p.Transcript, p.Cap); err != nil {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("dispatch failed: %w", err)}
				audit.LogAction(p.Transcript, "dispatch", nil, "FAILED: "+err.Error())
				return
			}
			audit.LogAction(p.Transcript, "dispatch", nil, "SUCCESS")
			e.Events <- Event{Type: EventToolExecuted, Payload: agent.Plan{Transcript: p.Transcript, Intent: "dispatch"}}
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
		e.busyLock.Lock()
		e.isBusy = false
		e.busyLock.Unlock()
		e.signalCommandDone()

	case EventError:
		log.Printf("Engine Error Event: %v", ev.Err)
		ui.SetState(ui.StateIdle)
		e.busyLock.Lock()
		e.isBusy = false
		e.busyLock.Unlock()
		e.signalCommandDone()
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

// signalCommandDone releases any waiter in TriggerAndWait (non-blocking).
func (e *Engine) signalCommandDone() {
	select {
	case e.commandDone <- struct{}{}:
	default:
	}
}

// TriggerAndWait fires a voice capture (as if the pill were clicked) and blocks until the
// command finishes or timeout elapses. Used by the wake-word loop to hand the mic back only
// after the command is done.
func (e *Engine) TriggerAndWait(timeout time.Duration) {
	// drain any stale completion signal
	select {
	case <-e.commandDone:
	default:
	}
	select {
	case ui.ListenTrigger <- struct{}{}:
	case <-time.After(2 * time.Second):
		return // engine not consuming triggers; give up
	}
	select {
	case <-e.commandDone:
	case <-time.After(timeout):
	}
}

