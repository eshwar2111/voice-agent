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
	"github.com/yourname/voice-agent/internal/executor"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/memory"
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

type Engine struct {
	Config       *config.Config
	Provider     llm.Provider
	Registry     *tools.Registry
	MemStore     *memory.Store
	MemRetriever *memory.Retriever
	RateLimiter  *security.RateLimiter
	Profile      *security.Profile

	History  []string
	Events   chan Event
	isBusy   bool // true while processing a command pipeline
	busyLock sync.Mutex
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
		Events:       make(chan Event, 100),
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
			return
		}

		e.busyLock.Lock()
		if e.isBusy {
			e.busyLock.Unlock()
			fmt.Println("⚠️  Already processing a command — ignoring trigger.")
			return
		}
		e.isBusy = true
		e.busyLock.Unlock()

		ui.SetState(ui.StateListening)

		go func() {
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
			e.Events <- Event{Type: EventTranscribed, Payload: transcript}
		}()

	case EventTranscribed:
		transcript := ev.Payload.(string)
		fmt.Printf("📝 Transcript: %s\n", transcript)

		// Phase 4: Route voice transcripts through the Orchestrator so they get
		// the same multi-agent decomposition + Phase 3 approval as typed commands.
		go func() {
			ui.SetState(ui.StateReasoning)

			executorApp := agent.NewExecutor(e.Registry)
			orch := agent.NewOrchestrator(e.Provider, executorApp)

			ui.SetState(ui.StateExecuting)
			fmt.Println("🚀 Orchestrating voice command…")

			if err := orch.Run(ctx, transcript); err != nil {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("orchestration failed: %w", err)}
				audit.LogAction(transcript, "voice_orchestration", nil, "FAILED: "+err.Error())
				return
			}

			audit.LogAction(transcript, "voice_orchestration", nil, "SUCCESS")
			e.Events <- Event{Type: EventToolExecuted, Payload: agent.Plan{
				Transcript: transcript,
				Intent:     "voice_orchestration",
			}}
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

	case EventError:
		log.Printf("Engine Error Event: %v", ev.Err)
		ui.SetState(ui.StateIdle)
		e.busyLock.Lock()
		e.isBusy = false
		e.busyLock.Unlock()
	}
}

