package dispatch

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/yourname/voice-agent/internal/agent"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/resolver"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
)

// Deps holds everything the tiered dispatcher needs. Construct once in main.
type Deps struct {
	Registry *tools.Registry
	Provider llm.Provider
	Profile  *security.Profile
	Resolver *resolver.Resolver
}

// localHits/cloudHits count ROUTING DECISIONS (which tier handled the
// input), not successful executions — a tier can still fail downstream
// (security rejection, tool error, orchestrator failure) after being counted.
var localHits, cloudHits int64

func LocalCount() int64 { return atomic.LoadInt64(&localHits) }
func CloudCount() int64 { return atomic.LoadInt64(&cloudHits) }

// Handle routes one command through Tier 0 (local) or Tier 1 (cloud).
// activeApp is the foreground process name (may be "").
func (d *Deps) Handle(ctx context.Context, input, activeApp string) error {
	if d.Resolver == nil || d.Registry == nil {
		return fmt.Errorf("dispatch: Deps not fully configured")
	}
	norm := resolver.Normalize(input, activeApp)
	if match, ok := d.Resolver.Resolve(norm); ok {
		atomic.AddInt64(&localHits, 1)
		log.Printf("[dispatch] TIER0 (%s) %q -> %d task(s)", match.Reason, input, len(match.Tasks))
		if err := d.enforceSecurity(match.Tasks); err != nil {
			return err
		}
		exec := agent.NewExecutor(d.Registry)
		return exec.ExecutePlan(ctx, agent.Plan{Transcript: input, Intent: "local_resolve", Tasks: match.Tasks})
	}
	atomic.AddInt64(&cloudHits, 1)
	log.Printf("[dispatch] TIER1 (cloud) %q", input)
	exec := agent.NewExecutor(d.Registry)
	orch := agent.NewOrchestrator(d.Provider, exec)
	return orch.Run(ctx, input)
}

// enforceSecurity applies the profile allow-list and per-tool confirmation once.
func (d *Deps) enforceSecurity(tasks []agent.Task) error {
	for _, task := range tasks {
		if d.Profile != nil && !d.Profile.IsAllowed(task.Tool) {
			return fmt.Errorf("tool %q not permitted in profile %q", task.Tool, d.Profile.Name)
		}
		tool, found := d.Registry.Get(task.Tool)
		if !found {
			return fmt.Errorf("tool %q not registered", task.Tool)
		}
		if tool.RequiresConfirmation() {
			if !security.RequestConfirmation(task.Tool, task.Params) {
				return fmt.Errorf("action %q cancelled by user", task.Tool)
			}
		}
	}
	return nil
}
