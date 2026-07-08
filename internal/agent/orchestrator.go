package agent

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/ui"
)

// SubGoal represents one decomposed unit of work with an explicit agent route.
type SubGoal struct {
	// Agent is the tool name to route this sub-goal to.
	// Recognized values: "google_workflow_agent", "spotify_workflow_agent",
	// "google_workspace_assistant", "spotify_assistant", or any single tool name.
	Agent   string `json:"agent"`
	Goal    string `json:"goal"`
	Context string `json:"context,omitempty"`
}

// Orchestrator is the top-level multi-agent coordinator.
// It decomposes a user's free-form request into sub-goals and delegates each
// one to the appropriate specialist agent or tool via the GraphExecutor.
type Orchestrator struct {
	Provider llm.Provider
	Executor *GraphExecutor
}

// NewOrchestrator creates an Orchestrator backed by the given LLM provider and executor.
func NewOrchestrator(provider llm.Provider, executor *GraphExecutor) *Orchestrator {
	return &Orchestrator{
		Provider: provider,
		Executor: executor,
	}
}

// Run is the primary entry-point.  It decomposes the user's request, executes
// each sub-goal in order (with per-agent approval check), and streams a brief
// status notification for each step.
func (o *Orchestrator) Run(ctx gocontext.Context, userText, sysContext string) error {
	ui.ShowNotification("Planning your request…")
	log.Printf("[Orchestrator] Decomposing: %q\n", userText)

	subGoals, err := o.decompose(ctx, userText, sysContext)
	if err != nil || len(subGoals) == 0 {
		// Fallback: treat the entire text as a single google_workflow_agent call
		log.Printf("[Orchestrator] Decompose failed or empty, falling back to single-agent: %v", err)
		subGoals = []SubGoal{{Agent: "google_workflow_agent", Goal: userText}}
	}

	// Fast path: single sub-goal with no orchestration overhead
	if len(subGoals) == 1 {
		if subGoals[0].Context == "" {
			subGoals[0].Context = sysContext
		}
		return o.execSubGoal(ctx, subGoals[0])
	}

	log.Printf("[Orchestrator] Dispatching %d sub-goals\n", len(subGoals))

	var results []string
	for i, sg := range subGoals {
		if sg.Context == "" {
			sg.Context = sysContext
		}
		ui.ShowNotification(fmt.Sprintf("Step %d/%d: %s…", i+1, len(subGoals), sg.Goal))
		log.Printf("[Orchestrator] Sub-goal %d: agent=%s, goal=%q\n", i+1, sg.Agent, sg.Goal)

		if err := o.execSubGoal(ctx, sg); err != nil {
			// Non-fatal: record failure and continue remaining sub-goals
			log.Printf("[Orchestrator] Sub-goal %d failed: %v", i+1, err)
			results = append(results, fmt.Sprintf("❌ Step %d (%s): %v", i+1, sg.Agent, err))
		} else {
			results = append(results, fmt.Sprintf("✅ Step %d (%s): done", i+1, sg.Agent))
		}
	}

	summary := strings.Join(results, "\n")
	log.Printf("[Orchestrator] Completed all sub-goals:\n%s\n", summary)
	ui.ShowNotification(fmt.Sprintf("Done — %d steps completed", len(subGoals)))

	return nil
}

// execSubGoal runs a single SubGoal by routing through GraphExecutor.
func (o *Orchestrator) execSubGoal(ctx gocontext.Context, sg SubGoal) error {
	agentName := sg.Agent
	if agentName == "" {
		agentName = "google_workflow_agent"
	}

	// Build a one-task plan so the existing ExecutePlan / Phase 3 approval loop runs unchanged.
	params, _ := json.Marshal(map[string]string{
		"goal":    sg.Goal,
		"context": sg.Context,
	})

	plan := Plan{
		Intent: sg.Goal,
		Tasks: []Task{
			{Tool: agentName, Params: params},
		},
	}

	return o.Executor.ExecutePlan(ctx, plan)
}

// ─────────────────────────────────────────────────────────────────────────────
// LLM-based decomposition
// ─────────────────────────────────────────────────────────────────────────────

const decompositionPrompt = `You are a task decomposition engine for a voice assistant.

Break the user's request into sub-goals, each routed to a specialist agent.

Available agents:
- "google_workflow_agent"  — multi-step Google Workspace tasks (Gmail, Calendar, Drive, Docs, Sheets, Slides)
- "spotify_workflow_agent" — multi-step Spotify tasks (play, queue, recommend, curate)
- "google_workspace_assistant" — single-step Workspace lookups or simple drafts
- "spotify_assistant"          — single-step Spotify actions (play a search, pause, volume)
- Any raw tool name listed in the tool registry (e.g. "web_search", "system_status", "speak", "open_website")

Rules:
1. Return ONLY a JSON array — no extra text.
2. Each element: {"agent":"<name>","goal":"<natural language sub-goal>","context":"<optional extra context>"}
3. Keep sub-goals tightly scoped. A Spotify action must NOT be in the same sub-goal as a Google action.
4. If the entire request maps to a single agent/tool, return exactly one element.
5. Preserve the user's original intent in "goal" — do not rephrase into commands.

Desktop context (may be empty):
%s

User request: %s

Return ONLY the JSON array.`

func (o *Orchestrator) decompose(ctx gocontext.Context, userText, sysContext string) ([]SubGoal, error) {
	prompt := fmt.Sprintf(decompositionPrompt, sysContext, userText)
	raw, err := o.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return nil, fmt.Errorf("LLM decomposition failed: %w", err)
	}

	// Extract first JSON array from the response
	raw = extractJSONArray(raw)
	var goals []SubGoal
	if err := json.Unmarshal([]byte(raw), &goals); err != nil {
		return nil, fmt.Errorf("failed to parse decomposition JSON %q: %w", raw, err)
	}
	return goals, nil
}

// extractJSONArray finds and returns the first [...] block in s.
func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || end <= start {
		return "[]"
	}
	return s[start : end+1]
}
