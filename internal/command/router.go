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
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
)

var (
	globalRegistry *tools.Registry
	globalProvider llm.Provider
	globalProfile  *security.Profile
	globalCtx      gocontext.Context
	globalCancel   gocontext.CancelFunc
)

func InitRouter(registry *tools.Registry, provider llm.Provider, profile *security.Profile) {
	globalRegistry = registry
	globalProvider = provider
	globalProfile = profile
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
	if err := validatePlan(plan); err != nil {
		log.Printf("Plan blocked: %v", err)
		return
	}

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

	toolSchemas := globalRegistry.DumpSchemasFiltered(func(name string, _ tools.Tool) bool {
		return isAllowed(name)
	})

	// Use Fast Path (ClassifyAndPlan) first
	classify, err := globalProvider.ClassifyAndPlan(globalCtx, prompt, toolSchemas, contextStr)
	var rawJSON string
	if err != nil || classify.NeedsScreen {
		// Fallback to screen-aware planning if needed or on error
		resp, err := globalProvider.GenerateIntent(globalCtx, llm.IntentRequest{
			UserText:      prompt,
			SystemContext: contextStr,
			ToolSchemas:   toolSchemas,
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
	if len(plan.Tasks) == 0 && parsedIntent.Intent != "" {
		plan.Tasks = append(plan.Tasks, agent.Task{Tool: parsedIntent.Intent, Params: parsedIntent.Parameters})
	}

	if err := validatePlan(plan); err != nil {
		log.Printf("AI plan blocked: %v", err)
		return
	}

	// Phase 4: Route through the Orchestrator instead of calling ExecutePlan directly.
	// For plans produced by the LLM's own ClassifyAndPlan pass we honour them as-is
	// (single-step dispatch); the Orchestrator's decomposer is only invoked when the
	// user sends a free-text "ai <request>" that contains no explicit tool selection.
	executor := agent.NewExecutor(globalRegistry)
	orch := agent.NewOrchestrator(globalProvider, executor)
	if err := orch.Run(globalCtx, prompt); err != nil {
		log.Printf("Orchestration failed: %v", err)
	}
}

func validatePlan(plan agent.Plan) error {
	for _, task := range plan.Tasks {
		if !isAllowed(task.Tool) {
			return fmt.Errorf("tool %q is not permitted in the current profile", task.Tool)
		}
		tool, found := globalRegistry.Get(task.Tool)
		if !found {
			return fmt.Errorf("tool %q is not registered", task.Tool)
		}
		if tool.RequiresConfirmation() {
			approved := security.RequestConfirmation(task.Tool, task.Params)
			if !approved {
				return fmt.Errorf("action blocked by user cancellation")
			}
		}
	}
	return nil
}

func isAllowed(toolName string) bool {
	if globalProfile == nil {
		return true
	}
	return globalProfile.IsAllowed(toolName)
}
