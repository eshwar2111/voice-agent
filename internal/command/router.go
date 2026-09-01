package command

import (
	gocontext "context"
	"fmt"
	"log"
	"strings"

	"github.com/yourname/voice-agent/internal/agent"
	agentctx "github.com/yourname/voice-agent/internal/context"
	"github.com/yourname/voice-agent/internal/dispatch"
	"github.com/yourname/voice-agent/internal/intent"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/resolver"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
	"github.com/yourname/voice-agent/internal/trust"
)

var (
	globalRegistry *tools.Registry
	globalProvider llm.Provider
	globalProfile  *security.Profile
	globalCtx      gocontext.Context
	globalCancel   gocontext.CancelFunc
	globalDispatch *dispatch.Deps
	globalTrusted  *trust.TrustedExecutor
)

// SetTrusted injects the shared trust layer into the router. Wiring both the
// dispatcher (voice/typed commands) and the AI-command executor to the same
// *trust.TrustedExecutor keeps every execution path behind the one-shot gate.
func SetTrusted(te *trust.TrustedExecutor) {
	globalTrusted = te
	if globalDispatch != nil {
		globalDispatch.Trusted = te
	}
}

func InitRouter(registry *tools.Registry, provider llm.Provider, profile *security.Profile) {
	globalRegistry = registry
	globalProvider = provider
	globalProfile = profile
	globalCtx, globalCancel = gocontext.WithCancel(gocontext.Background())

	globalDispatch = &dispatch.Deps{
		Registry: registry,
		Provider: provider,
		Profile:  profile,
		Resolver: resolver.Default(),
	}
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

	cap := takePendingCapture()
	if err := globalDispatch.Handle(globalCtx, input, cap); err != nil {
		log.Printf("dispatch failed: %v", err)
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

	// Execute the plan we just built and validated. Routing through orch.Run
	// here would re-plan from scratch, discarding both the upfront planning and
	// the validatePlan gate above — so when the plan already has tasks we run it
	// directly through the executor, which applies the trust layer and the
	// profile Allow gate. Only a genuinely empty plan (no tool selection) is
	// handed to the Orchestrator to be decomposed.
	executor := agent.NewExecutor(globalRegistry)
	executor.Trusted = globalTrusted
	if globalProfile != nil {
		executor.Allow = globalProfile.IsAllowed
	}
	if len(plan.Tasks) > 0 {
		if err := executor.ExecutePlan(globalCtx, plan); err != nil {
			log.Printf("Plan execution failed: %v", err)
		}
		return
	}
	orch := agent.NewOrchestrator(globalProvider, executor)
	if err := orch.Run(globalCtx, prompt, contextStr); err != nil {
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
