package agent

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/yourname/voice-agent/internal/tools"
	"github.com/yourname/voice-agent/internal/ui"
)

type GraphExecutor struct {
	Registry *tools.Registry
}

func NewExecutor(registry *tools.Registry) *GraphExecutor {
	return &GraphExecutor{
		Registry: registry,
	}
}

// ExecutePlan runs tasks sequentially, passing data context between tools.
func (e *GraphExecutor) ExecutePlan(ctx gocontext.Context, plan Plan) error {
	log.Printf("Starting execution of Plan: %s (Tasks: %d)\n", plan.Intent, len(plan.Tasks))

	memory := NewMemory()
	var lastOutput string

	for i, task := range plan.Tasks {
		log.Printf("  [Step %d/%d] Executing Tool: %s\n", i+1, len(plan.Tasks), task.Tool)

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

		// Save output to short-term memory to flow to the next node
		memory.Set(task.Tool, result)
		lastOutput = result

		log.Printf("    ✅ Tool success. Output length: %d chars\n", len(result))
		if len(result) > 0 {
			if len(result) < 200 {
				fmt.Printf("    ↳ %s\n", result)
			}
			// Let the final node output persist on the screen overlay
			if i == len(plan.Tasks)-1 {
				go func() {
					importUIAndShowOutput(result)
				}()
			}
		}
	}

	log.Printf("Plan %s execution completed successfully.\n", plan.Intent)
	return nil
}

// importUIAndShowOutput breaks the circular dependency between agent and UI by directly invoking it here
func importUIAndShowOutput(text string) {
	ui.ShowOutputOverlay(text)
}
