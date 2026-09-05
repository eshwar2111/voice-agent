package dispatch

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yourname/voice-agent/internal/agent"
	agentctx "github.com/yourname/voice-agent/internal/context"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/resolver"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
	"github.com/yourname/voice-agent/internal/trust"
	"github.com/yourname/voice-agent/internal/ui"
)

// Deps holds everything the tiered dispatcher needs. Construct once in main.
type Deps struct {
	Registry *tools.Registry
	Provider llm.Provider
	Profile  *security.Profile
	Resolver *resolver.Resolver
	// Trusted, when non-nil, wraps every executed plan with the trust layer.
	Trusted *trust.TrustedExecutor

	// session carries compact cross-turn task context (TaskSession v1) into
	// Tier-1 planning so follow-ups continue the same task. Zero value is ready.
	session taskSession
}

// localHits/cloudHits count ROUTING DECISIONS (which tier handled the
// input), not successful executions — a tier can still fail downstream
// (security rejection, tool error, orchestrator failure) after being counted.
var localHits, cloudHits int64

func LocalCount() int64 { return atomic.LoadInt64(&localHits) }
func CloudCount() int64 { return atomic.LoadInt64(&cloudHits) }

// Handle routes one command through Tier 0 (local) or Tier 1 (cloud).
// cap is the ambient desktop context (may be a zero-value Capture).
func (d *Deps) Handle(ctx context.Context, input string, cap agentctx.Capture) error {
	if d.Resolver == nil || d.Registry == nil {
		return fmt.Errorf("dispatch: Deps not fully configured")
	}
	// Silence is not a command. Whisper transcribes non-speech audio as a
	// bracketed marker ("[BLANK_AUDIO]"), and dispatching that sent it to the
	// cloud orchestrator, which planned "apologise and ask the user to repeat"
	// — a token-burning round trip triggered by an empty room. Drop it quietly:
	// returning an error here would surface "dispatch failed" in the UI for
	// what is simply nothing having been said.
	if isNonSpeech(input) {
		log.Printf("[dispatch] ignoring non-speech transcript %q", input)
		return nil
	}
	// TaskSession v1: capture the ongoing-task context (from prior turns) BEFORE
	// recording this turn, then record this turn for the next one. Context is
	// only injected into Tier-1 planning below; Tier-0 stays deterministic.
	now := time.Now()
	taskCtx := d.session.contextIfActive(now)
	d.session.record(input, now)

	// Interaction-mode classifier: the explicit up-front "simple vs long task"
	// decision so long/bulk requests are handled with a progress presentation
	// rather than a one-shot status. (Long tools like research drive their own
	// progress card; this makes the decision visible and available to routing.)
	mode := classifyInteraction(input)
	log.Printf("[dispatch] mode=%s %q", mode, input)

	norm := resolver.Normalize(input, cap.AppName)
	if match, ok := d.Resolver.Resolve(norm); ok {
		atomic.AddInt64(&localHits, 1)
		log.Printf("[dispatch] TIER0 (%s) %q -> %d task(s)", match.Reason, input, len(match.Tasks))
		if err := d.enforceSecurity(match.Tasks); err != nil {
			return err
		}
		exec := agent.NewExecutor(d.Registry)
		exec.Trusted = d.Trusted
		if d.Profile != nil {
			exec.Allow = d.Profile.IsAllowed
		}
		return exec.ExecutePlan(ctx, agent.Plan{Transcript: input, Intent: "local_resolve", Tasks: match.Tasks})
	}
	atomic.AddInt64(&cloudHits, 1)

	// Tier 1: visual sub-path when the request implies on-screen context.
	if agentctx.NeedsScreenshot(input) {
		log.Printf("[dispatch] TIER1 (visual) %q", input)
		shot := cap.WithScreenshot()
		prompt := fmt.Sprintf("Desktop context:\n%s\n\nUser request: %s\n\nAnswer helpfully and concisely.",
			shot.String(), input)
		answer, err := d.Provider.Generate(ctx, prompt, [][]byte{shot.Screenshot})
		if err != nil {
			return err
		}
		ui.ShowOutputOverlay(answer)
		return nil
	}

	// Tier 1: text path via the orchestrator, enriched with captured context.
	log.Printf("[dispatch] TIER1 (cloud) %q", input)
	exec := agent.NewExecutor(d.Registry)
	exec.Trusted = d.Trusted
	if d.Profile != nil {
		exec.Allow = d.Profile.IsAllowed
	}
	orch := agent.NewOrchestrator(d.Provider, exec)
	sysCtx := cap.String()
	if taskCtx != "" {
		sysCtx = taskCtx + "\n\n" + sysCtx
	}
	return orch.Run(ctx, input, sysCtx)
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

// isNonSpeech reports whether a transcript carries no actual speech.
//
// Whisper marks non-speech audio with a bracketed token — "[BLANK_AUDIO]",
// "[SILENCE]", "[MUSIC]" — rather than returning an empty string. Only a
// transcript that is ENTIRELY such a marker counts: brackets appearing inside
// real speech ("play [something] by the band") must still dispatch.
func isNonSpeech(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return true
	}
	// A transcript of only punctuation/ellipsis is silence too.
	if strings.Trim(t, ".,!?;:-— \t") == "" {
		return true
	}
	if (strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")) ||
		(strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")")) {
		inner := strings.ToLower(strings.Trim(t, "[]() \t_"))
		switch inner {
		case "blank_audio", "blank audio", "silence", "music", "noise",
			"inaudible", "no audio", "sound", "applause", "laughter":
			return true
		}
	}
	return false
}
