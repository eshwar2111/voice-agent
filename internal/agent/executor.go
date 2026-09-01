package agent

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/yourname/voice-agent/internal/tools"
	"github.com/yourname/voice-agent/internal/trust"
	"github.com/yourname/voice-agent/internal/ui"
)

type GraphExecutor struct {
	Registry *tools.Registry
	// Trusted, when non-nil, wraps every plan with the trust layer (risk
	// classification, one-shot approval gate, verification, recovery ladder).
	// When nil, ExecutePlan runs the legacy loop with identical behavior.
	Trusted *trust.TrustedExecutor
	// Allow, when non-nil, gates every tool against the active security
	// profile's allow-list before it runs. nil means allow all (unchanged
	// legacy behavior). This is enforced on BOTH the trust path (per-tool,
	// covering future re-plans) and the legacy per-task loop, so LLM-
	// decomposed plans on the Tier-1 path cannot execute a tool the profile
	// forbids.
	Allow func(tool string) bool
}

func NewExecutor(registry *tools.Registry) *GraphExecutor {
	return &GraphExecutor{
		Registry: registry,
	}
}

// ExecutePlan runs tasks sequentially, passing data context between tools.
func (e *GraphExecutor) ExecutePlan(ctx gocontext.Context, plan Plan) error {
	log.Printf("Starting execution of Plan: %s (Tasks: %d)\n", plan.Intent, len(plan.Tasks))

	// Trust layer delegation. When wired, the TrustedExecutor owns the per-step
	// loop (gate → run → verify → recover). When nil, fall through to the legacy
	// loop below, which behaves exactly as before.
	if e.Trusted != nil {
		steps := make([]trust.Step, len(plan.Tasks))
		for i, tk := range plan.Tasks {
			steps[i] = trust.Step{Tool: tk.Tool, Params: tk.Params, Goal: tk.Tool}
		}
		// Supply the tool-running seam only if the caller hasn't already set one.
		if e.Trusted.Exec == nil {
			reg := e.Registry
			allow := e.Allow
			e.Trusted.Exec = func(ctx gocontext.Context, tool string, params json.RawMessage) (string, error) {
				if allow != nil && !allow(tool) {
					return "", fmt.Errorf("tool %q not permitted by security profile", tool)
				}
				return RunTool(ctx, reg, tool, params)
			}
		}
		rep, err := e.Trusted.Run(ctx, steps, plan.Transcript)
		if err != nil {
			return err
		}
		for _, done := range rep.Completed {
			log.Printf("    ✅ [trust] completed: %s\n", done)
		}
		if rep.Aborted {
			log.Printf("[trust] stopped: %s (completed %d/%d steps)\n", rep.FailNote, len(rep.Completed), len(plan.Tasks))
			return fmt.Errorf("plan stopped: %s", rep.FailNote)
		}
		// Surface the final step's output on the overlay (and, for voice commands,
		// speak it) — the legacy loop below does this for its last node; the trust
		// path must too, or a command like "what time is it" runs but nothing is
		// ever shown or spoken.
		if disp := displayOutput(rep.LastOutput); disp != "" {
			go importUIAndShowOutput(disp)
		}
		log.Printf("Plan %s execution completed successfully (trust).\n", plan.Intent)
		return nil
	}

	memory := NewMemory()
	var lastOutput string

	for i, task := range plan.Tasks {
		log.Printf("  [Step %d/%d] Executing Tool: %s\n", i+1, len(plan.Tasks), task.Tool)

		if e.Allow != nil && !e.Allow(task.Tool) {
			return fmt.Errorf("tool %q not permitted by security profile", task.Tool)
		}

		tool, found := e.Registry.Get(task.Tool)
		if !found {
			errDesc := fmt.Errorf("tool not found in registry: %s", task.Tool)
			log.Printf("    ❌ %v. Aborting plan.\n", errDesc)
			return errDesc
		}

		// Inject {PREVIOUS_OUTPUT} alias into params if downstream tools depend on it
		var injectedParams json.RawMessage = task.Params

		if len(task.Params) > 0 && strings.Contains(string(task.Params), "{PREVIOUS_OUTPUT}") {
			var parsed map[string]interface{}
			if err := json.Unmarshal(task.Params, &parsed); err == nil {
				for k, v := range parsed {
					if strVal, ok := v.(string); ok {
						if strings.Contains(strVal, "{PREVIOUS_OUTPUT}") {
							parsed[k] = strings.ReplaceAll(strVal, "{PREVIOUS_OUTPUT}", lastOutput)
						}
					}
				}
				if b, err := json.Marshal(parsed); err == nil {
					injectedParams = b
				}
			}
		}

		result, err := tool.Execute(ctx, injectedParams)
		if err != nil {
			log.Printf("    ❌ Tool '%s' execution failed: %v\n", task.Tool, err)
			return err
		}

		// Phase 3: Handle Interactive Approval Handshake
		tr := tools.ParseToolResult(result)
		if tr.Artifacts["type"] == "workflow_approval" {
			log.Printf("    ⏸️ Approval required for workflow: %s\n", tr.Summary)
			
			// Show confirmation card in UI
			cardJSON, _ := json.Marshal(tr.Artifacts)
			approved := ui.RequestConfirmationCard(string(cardJSON))
			
			if !approved {
				log.Printf("    🚫 User rejected workflow. Aborting plan.\n")
				return fmt.Errorf("workflow execution cancelled by user")
			}

			// Approved! Re-execute the same tool with the 'approved_plan' injected
			log.Printf("    ✅ Workflow approved. Re-executing with plan context...\n")
			
			rawPlan, _ := json.Marshal(tr.Artifacts["raw"])
			var paramsMap map[string]interface{}
			json.Unmarshal(injectedParams, &paramsMap)
			paramsMap["approved_plan"] = string(rawPlan)
			
			finalParams, _ := json.Marshal(paramsMap)
			result, err = tool.Execute(ctx, finalParams)
			if err != nil {
				log.Printf("    ❌ Tool '%s' execution failed after approval: %v\n", task.Tool, err)
				return err
			}
		}

		// Save output to short-term memory to flow to the next node
		memory.Set(task.Tool, result)
		lastOutput = result

		// Parse structured output — only use Summary for display/logging
		parsed := tools.ParseToolResult(result)
		displayText := parsed.Summary
		if displayText == "" {
			displayText = result
		}

		log.Printf("    ✅ Tool success. Output length: %d chars\n", len(result))
		if len(displayText) > 0 {
			if len(displayText) < 200 {
				fmt.Printf("    ↳ %s\n", displayText)
			}
			// Let the final node output persist on the screen overlay
			if i == len(plan.Tasks)-1 {
				go func(text string) {
					importUIAndShowOutput(text)
				}(displayText)
			}
		}
	}

	log.Printf("Plan %s execution completed successfully.\n", plan.Intent)
	return nil
}

// RunTool executes a single tool by name and returns a clean (result, error).
// It performs the registry lookup, calls tool.Execute, runs the Phase-3
// workflow_approval handshake (show the confirmation card, then re-execute with
// the approved plan injected), and folds a failure-encoding ToolResult into a
// non-nil error so callers (notably the trust Verifier) see execErr directly.
//
// Params must already have any {PREVIOUS_OUTPUT} substitution applied by the
// caller; RunTool does not inject data-flow variables.
func RunTool(ctx gocontext.Context, reg *tools.Registry, tool string, params json.RawMessage) (string, error) {
	t, found := reg.Get(tool)
	if !found {
		return "", fmt.Errorf("tool not found in registry: %s", tool)
	}

	result, err := t.Execute(ctx, params)
	if err != nil {
		return "", err
	}

	// Phase 3: Interactive Approval Handshake.
	tr := tools.ParseToolResult(result)
	if tr.Artifacts["type"] == "workflow_approval" {
		cardJSON, _ := json.Marshal(tr.Artifacts)
		approved := ui.RequestConfirmationCard(string(cardJSON))
		if !approved {
			return "", fmt.Errorf("workflow execution cancelled by user")
		}

		// Approved — re-execute the same tool with the 'approved_plan' injected.
		rawPlan, _ := json.Marshal(tr.Artifacts["raw"])
		var paramsMap map[string]interface{}
		json.Unmarshal(params, &paramsMap)
		if paramsMap == nil {
			paramsMap = map[string]interface{}{}
		}
		paramsMap["approved_plan"] = string(rawPlan)

		finalParams, _ := json.Marshal(paramsMap)
		result, err = t.Execute(ctx, finalParams)
		if err != nil {
			return "", err
		}
		tr = tools.ParseToolResult(result)
	}

	// Fold a failure-encoding ToolResult into an error so trust's Verifier can
	// see execErr rather than parsing tools.ToolResult itself. No current tool
	// emits type:"error", so this is inert for existing tools.
	if tr.Artifacts["type"] == "error" {
		msg := tr.Summary
		if msg == "" {
			msg = "tool reported failure: " + tool
		}
		return result, fmt.Errorf("%s", msg)
	}

	return result, nil
}

// displayOutput picks the human-facing text from a raw tool result: the
// ToolResult Summary when present, else the raw string. Empty stays empty.
func displayOutput(raw string) string {
	if raw == "" {
		return ""
	}
	if s := tools.ParseToolResult(raw).Summary; s != "" {
		return s
	}
	return raw
}

// importUIAndShowOutput breaks the circular dependency between agent and UI by directly invoking it here
func importUIAndShowOutput(text string) {
	ui.ShowOutputOverlay(text)
}

