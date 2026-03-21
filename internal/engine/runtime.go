package engine

import (
	"context"
	"encoding/json"
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
	"github.com/yourname/voice-agent/internal/executor"
	"github.com/yourname/voice-agent/internal/intent"
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

		go func() {
			plan, err := e.planExecution(ctx, transcript)
			if err != nil {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("planning failed: %w", err)}
				return
			}
			e.Events <- Event{Type: EventPlanReady, Payload: plan}
		}()

	case EventPlanReady:
		plan := ev.Payload.(agent.Plan)
		ui.SetState(ui.StateExecuting)

		go func() {
			fmt.Println("🚀 Executing...")
			executorApp := agent.NewExecutor(e.Registry)
			err := executorApp.ExecutePlan(ctx, plan)

			tasksJSON, _ := json.Marshal(plan.Tasks)
			if err != nil {
				e.Events <- Event{Type: EventError, Err: fmt.Errorf("execution failed: %w", err)}
				audit.LogAction(plan.Transcript, plan.Intent, tasksJSON, "FAILED: "+err.Error())
				return
			}
			audit.LogAction(plan.Transcript, plan.Intent, tasksJSON, "SUCCESS")

			e.Events <- Event{Type: EventToolExecuted, Payload: plan}
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

func (e *Engine) planExecution(rootCtx context.Context, transcript string) (agent.Plan, error) {
	fmt.Println("🔍 Gathering desktop context...")

	// Gather context and search memories in parallel
	type contextResult struct {
		sysCtx *agentctx.AgentContext
	}
	type memResult struct {
		mems []memory.Memory
		err  error
	}

	ctxChan := make(chan contextResult, 1)
	memChan := make(chan memResult, 1)

	go func() {
		ctxChan <- contextResult{sysCtx: agentctx.BuildContext()}
	}()
	go func() {
		mems, err := e.MemRetriever.Search(transcript, 5)
		memChan <- memResult{mems: mems, err: err}
	}()

	cResult := <-ctxChan
	mResult := <-memChan

	var processName, windowTitle string
	if cResult.sysCtx.Window != nil {
		processName = cResult.sysCtx.Window.ProcessName
		windowTitle = cResult.sysCtx.Window.WindowTitle
	}
	contextStr := fmt.Sprintf("Active App: %s\nWindow Title: %s\nClipboard Preview:\n%s\n", processName, windowTitle, cResult.sysCtx.Clipboard)

	if mResult.err == nil && len(mResult.mems) > 0 {
		memBlock := memory.FormatForPrompt(mResult.mems)
		fmt.Printf("🧠 Recalled %d memories\n", len(mResult.mems))
		contextStr += "\n" + memBlock
	}

	if len(e.History) > 0 {
		contextStr += "\nRecent Conversation History:\n"
		for _, h := range e.History {
			contextStr += h + "\n"
		}
	}

	// Check rate limit before expensive LLM calls
	if !e.RateLimiter.Allow() {
		return agent.Plan{}, fmt.Errorf("rate limit exceeded — please wait before sending another command")
	}

	fmt.Println("⚡ [FAST PATH] Classifying command without screenshot...")
	classifyCtx, classifyCancel := context.WithTimeout(rootCtx, 15*time.Second)
	classify, err := e.Provider.ClassifyAndPlan(classifyCtx, transcript, e.Registry.DumpSchemas(), contextStr)
	classifyCancel()

	var rawJSON string
	if err != nil {
		log.Printf("Classification failed: %v. Falling back to screen-aware path.", err)
		classify.NeedsScreen = true
	}

	if !classify.NeedsScreen {
		fmt.Println("✅ [FAST PATH] Command doesn't need screen. Executing directly!")
		rawJSON = classify.RawJSON
	} else {
		fmt.Println("📸 [SCREEN PATH] Command needs screen context. Capturing...")
		imgBytes, err := executor.CaptureScreen()
		if err != nil {
			return agent.Plan{}, fmt.Errorf("failed to capture screen: %w", err)
		}

		streamParser := intent.NewStreamParser()
		textDeltaChan := make(chan string, 100)

		go func() {
			for chunk := range textDeltaChan {
				streamParser.Feed(chunk)
			}
			streamParser.Close()
		}()

		go func() {
			var fullText string
			for textFrag := range streamParser.TextStream {
				fullText += textFrag
				ui.ShowOutputOverlay(fullText)
			}
		}()

		fmt.Println("🧠 [SCREEN PATH] Sending transcript + screenshot to Gemini...")
		reqCtx, reqCancel := context.WithTimeout(rootCtx, time.Duration(e.Config.TimeoutSeconds)*time.Second)
		resp, err := e.Provider.StreamGenerateIntent(reqCtx, llm.IntentRequest{
			UserText:      transcript,
			SystemContext: contextStr,
			ToolSchemas:   e.Registry.DumpSchemas(),
			Image:         imgBytes,
		}, textDeltaChan)
		reqCancel()

		if err != nil {
			return agent.Plan{}, fmt.Errorf("failed generating intent: %w", err)
		}
		rawJSON = resp.RawJSON
	}

	fmt.Println("✅ Parsed JSON Intent:")
	fmt.Println(rawJSON)

	parsedIntent, err := intent.ParseIntentJSON(rawJSON)
	if err != nil {
		return agent.Plan{}, fmt.Errorf("failed to parse intent JSON: %w", err)
	}

	plan := agent.Plan{
		Transcript: transcript,
		Intent:     parsedIntent.Intent,
		Tasks:      make([]agent.Task, 0, len(parsedIntent.Tasks)),
	}
	for _, t := range parsedIntent.Tasks {
		plan.Tasks = append(plan.Tasks, agent.Task{Tool: t.Tool, Params: t.Params})
	}

	if len(plan.Tasks) == 0 && parsedIntent.Intent != "" {
		plan.Tasks = append(plan.Tasks, agent.Task{Tool: parsedIntent.Intent, Params: parsedIntent.Parameters})
	}

	blocked := false
	for _, task := range plan.Tasks {
		if !e.Profile.IsAllowed(task.Tool) {
			log.Printf("Security Blocked: Tool '%s' is not permitted in %s profile", task.Tool, e.Profile.Name)
			blocked = true
			break
		}
	}
	if blocked {
		return agent.Plan{}, fmt.Errorf("plan blocked by security profile")
	}

	for _, task := range plan.Tasks {
		tool, found := e.Registry.Get(task.Tool)
		if found && tool.RequiresConfirmation() {
			approved := security.RequestConfirmation(task.Tool, task.Params)
			if !approved {
				return agent.Plan{}, fmt.Errorf("action blocked by user cancellation")
			}
		}
	}

	return plan, nil
}
