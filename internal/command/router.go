package command

import (
	gocontext "context"
	"fmt"
	"log"
	"strings"

	"github.com/yourname/voice-agent/internal/agent"
	agentctx "github.com/yourname/voice-agent/internal/context"
	"github.com/yourname/voice-agent/internal/intent"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/tools"
)

var (
	globalRegistry *tools.Registry
	globalProvider llm.Provider
	globalCtx      gocontext.Context
	globalCancel   gocontext.CancelFunc
)

func InitRouter(registry *tools.Registry, provider llm.Provider) {
	globalRegistry = registry
	globalProvider = provider
	globalCtx, globalCancel = gocontext.WithCancel(gocontext.Background())
}

// Shutdown cancels the router's context, stopping in-flight commands.
func Shutdown() {
	if globalCancel != nil {
		globalCancel()
	}
}

func ProcessCommand(input string) {
	AddToHistory(input)

	if strings.HasPrefix(input, "ai ") {
		RunAICommand(input)
		return
	}

	intent := Parse(input)
	if intent.Intent == "unknown" {
		log.Printf("Unknown command layout: %s", input)
		return
	}

	// New Phase 4: Translate Intent -> Automation Plan (Task Graph)
	plan := agent.CreatePlan(intent.Intent, intent.Params)

	// Use the global context so commands can be cancelled on shutdown
	executor := agent.NewExecutor(globalRegistry)
	if err := executor.ExecutePlan(globalCtx, plan); err != nil {
		log.Printf("Plan execution failed: %v", err)
	}
}

func RunAICommand(input string) {
	prompt := strings.TrimPrefix(input, "ai ")

	log.Printf("Running AI Command: %s", prompt)

	// Build context (Time, Window, Clipboard)
	sysCtx := agentctx.BuildContext()
	contextStr := fmt.Sprintf("Current Time: %s\nActive App: %s\nWindow Title: %s\nClipboard Preview: %s\n",
		sysCtx.Time.Format("2006-01-02 15:04:05"),
		sysCtx.Window.ProcessName,
		sysCtx.Window.WindowTitle,
		sysCtx.Clipboard,
	)

	// Use Fast Path (ClassifyAndPlan) first
	classify, err := globalProvider.ClassifyAndPlan(globalCtx, prompt, globalRegistry.DumpSchemas(), contextStr)
	var rawJSON string
	if err != nil || classify.NeedsScreen {
		// Fallback to screen-aware planning if needed or on error
		resp, err := globalProvider.GenerateIntent(globalCtx, llm.IntentRequest{
			UserText:      prompt,
			SystemContext: contextStr,
			ToolSchemas:   globalRegistry.DumpSchemas(),
		})
		if err != nil {
			log.Printf("AI Planning Failed: %v", err)
			return
		}
		rawJSON = resp.RawJSON
	} else {
		rawJSON = classify.RawJSON
	}

	parsedIntent, err := intent.ParseIntentJSON(rawJSON)
	if err != nil {
		log.Printf("Failed to parse AI intent: %v", err)
		return
	}

	plan := agent.Plan{
		Transcript: prompt,
		Intent:     parsedIntent.Intent,
		Tasks:      make([]agent.Task, 0, len(parsedIntent.Tasks)),
	}
	for _, t := range parsedIntent.Tasks {
		plan.Tasks = append(plan.Tasks, agent.Task{Tool: t.Tool, Params: t.Params})
	}

	// Execute the plan
	executor := agent.NewExecutor(globalRegistry)
	if err := executor.ExecutePlan(globalCtx, plan); err != nil {
		log.Printf("AI Plan execution failed: %v", err)
	}
}
