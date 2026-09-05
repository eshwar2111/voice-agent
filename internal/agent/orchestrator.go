package agent

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
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
		// Fallback: answer directly with the LLM. The old fallback routed EVERY
		// unclassified request to google_workflow_agent, so a plain question like
		// "explain quantum tunneling" tried to use Google and failed on an OAuth
		// token — surfacing as a bogus "Step failed / Approve" card. A general
		// query belongs on the answer path, never on an app agent.
		log.Printf("[Orchestrator] Decompose failed or empty, answering directly: %v", err)
		subGoals = []SubGoal{{Agent: "answer", Goal: userText}}
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
		// Feed the agent.run progress ring REAL step data (1-based index, total,
		// and the sub-goal as the label) rather than an opaque "Step x/y" status
		// string. This replaces the prior ShowNotification here: pushing narration
		// through ShowNotification would overwrite agent.run's data on the JS side
		// and drop the step/total the ring needs, so the two must not both fire.
		ui.SetAgentProgress(i+1, len(subGoals), sg.Goal)
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
		agentName = "answer"
	}

	// General-answer path: a question/explanation/chit-chat that needs no app or
	// user data. Answer directly with the LLM and surface it (ShowOutputOverlay
	// also speaks it for voice commands). Never routes to an app agent, so it can
	// never fail on an OAuth token.
	switch agentName {
	case "answer", "chat", "respond", "general":
		prompt := "You are a concise, helpful voice assistant. Answer conversationally in a few sentences (it may be read aloud).\n\n"
		if sg.Context != "" {
			prompt += "Context:\n" + sg.Context + "\n\n"
		}
		prompt += sg.Goal
		// Stream the answer so text appears as it's generated instead of popping
		// in after a pause. The final EndStreamedAnswer re-renders the full text
		// authoritatively, so a partial/failed stream never leaves a broken card.
		ui.BeginStreamedAnswer()
		ch := make(chan string, 32)
		done := make(chan struct{})
		go func() {
			for d := range ch {
				ui.PushAnswerDelta(d)
			}
			close(done)
		}()
		ans, err := o.Provider.StreamGenerate(ctx, prompt, nil, ch)
		<-done // all deltas delivered before the authoritative final render
		if err != nil && strings.TrimSpace(ans) == "" {
			return err
		}
		ui.EndStreamedAnswer(ans)
		return nil
	}

	// Build the tool's params. The high-level agents take a {goal, context} shim
	// and do their own internal planning. A RAW tool (create_file, open_file, …)
	// needs its ACTUAL arguments — passing {goal, context} to create_file gave it
	// no "filename" and it hard-failed ("create_file requires a filename") even
	// though the request clearly named the file. Generate real args from the
	// tool's own JSON schema.
	tool, found := o.Executor.Registry.Get(agentName)
	if !found {
		return fmt.Errorf("tool not found in registry: %s", agentName)
	}
	var params json.RawMessage
	if toolTakesGoal(tool.Parameters()) {
		// The high-level workflow agents accept a free-form {goal, context}.
		params, _ = json.Marshal(map[string]string{"goal": sg.Goal, "context": sg.Context})
	} else {
		// Everything else (research→query, *_assistant→request, create_file→
		// filename, …) needs its real, schema-shaped arguments.
		p, err := o.generateToolParams(ctx, agentName, sg)
		if err != nil {
			return err
		}
		params = p
	}

	plan := Plan{
		Intent: sg.Goal,
		Tasks: []Task{
			{Tool: agentName, Params: params},
		},
	}

	return o.Executor.ExecutePlan(ctx, plan)
}

// toolTakesGoal reports whether a tool accepts the free-form {goal, context}
// shim — i.e. its schema declares a "goal" parameter (the high-level workflow
// agents). Every other tool needs real schema-shaped args instead; hardcoding a
// list got this wrong (research takes "query", the assistants take "request"),
// so detect it from the schema.
func toolTakesGoal(schema string) bool {
	var m map[string]any
	if json.Unmarshal([]byte(schema), &m) != nil {
		return false
	}
	if props, ok := m["properties"].(map[string]any); ok {
		_, has := props["goal"]
		return has
	}
	_, has := m["goal"]
	return has
}

// generateToolParams builds a raw tool's arguments from its schema and the
// request. It asks the LLM for what the request specifies (WITHOUT inventing
// values for genuinely-unstated required fields), then asks the USER for any
// required field still missing — so "create a file with hello world" prompts
// "What should I name the file?" instead of failing or guessing.
func (o *Orchestrator) generateToolParams(ctx gocontext.Context, tool string, sg SubGoal) (json.RawMessage, error) {
	t, found := o.Executor.Registry.Get(tool)
	if !found {
		return nil, fmt.Errorf("tool not found in registry: %s", tool)
	}
	prompt := fmt.Sprintf(`Produce the arguments for a single tool call as JSON.

Tool: %s
What it does: %s
Argument schema (JSON Schema):
%s

User request: %s
Extra context (may be empty): %s

Rules:
- Return ONLY a single JSON object of arguments. No prose, no markdown fences.
- Fill every field the request actually specifies or clearly implies.
- If a REQUIRED field is genuinely not specified by the request, OMIT it
  entirely — do NOT invent a value. The user will be asked for it.`,
		tool, t.Description(), t.Parameters(), sg.Goal, sg.Context)

	raw, err := o.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return nil, err
	}
	obj := map[string]any{}
	_ = json.Unmarshal([]byte(extractJSONObject(raw)), &obj) // best-effort; ask fills the gaps

	// Ask the user for any required field the request didn't supply.
	for _, field := range requiredFields(t.Parameters()) {
		if !isBlank(obj[field]) {
			continue
		}
		answer, ok := ui.AskText(questionFor(field))
		if !ok || strings.TrimSpace(answer) == "" {
			return nil, fmt.Errorf("cancelled — I still need the %s", field)
		}
		obj[field] = strings.TrimSpace(answer)
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("couldn't work out the details for %s", tool)
	}
	return b, nil
}

// isBlank reports whether a generated argument value is missing or empty.
func isBlank(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// requiredFields extracts the required argument names from a tool's Parameters()
// string, handling BOTH shapes used in this codebase:
//   1. Proper JSON Schema: {"type":"object","properties":{…},"required":["x"]}
//   2. Ad-hoc flat form:    {"path":"string (required - …)","content":"…"}
// Missing this second form is why write_file failed on "missing path parameter"
// instead of asking — its schema has no "required" array.
func requiredFields(schema string) []string {
	var m map[string]any
	if json.Unmarshal([]byte(schema), &m) != nil {
		return nil
	}
	if _, isSchema := m["properties"]; isSchema {
		var s struct {
			Required []string `json:"required"`
		}
		_ = json.Unmarshal([]byte(schema), &s)
		return s.Required
	}
	// Flat form: a field is required when its description mentions "required".
	var req []string
	for k, v := range m {
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), "required") {
			req = append(req, k)
		}
	}
	return req
}

// questionFor phrases a friendly clarification question for a schema field.
func questionFor(field string) string {
	switch field {
	case "filename", "file_name", "name":
		return "What should I name the file?"
	case "path", "file_path":
		return "Which file or folder?"
	case "content", "text", "body":
		return "What should it say?"
	case "query", "q", "search":
		return "What should I search for?"
	case "app_name", "app":
		return "Which app?"
	case "url", "website":
		return "Which website or URL?"
	case "message":
		return "What's the message?"
	}
	return "What should the " + field + " be?"
}

// extractJSONObject returns the first {...} block in s, or "{}".
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end <= start {
		return "{}"
	}
	return s[start : end+1]
}

// ─────────────────────────────────────────────────────────────────────────────
// LLM-based decomposition
// ─────────────────────────────────────────────────────────────────────────────

const decompositionPrompt = `You are a task decomposition engine for a voice assistant.

Break the user's request into sub-goals, each routed to a specialist agent.

Available agents:
- "answer" — general questions, explanations, definitions, advice, calculations,
  writing, or chit-chat answerable from general knowledge, needing NO app and
  NONE of the user's private data. THE DEFAULT for anything not clearly another
  agent's job.
- "research" — questions needing CURRENT info from the web (weather, news,
  prices, sports scores, "right now", "today", live facts). It fetches and
  returns a concise synthesized answer. PREFER this over web_search for any
  question — web_search only dumps raw links.
- "web_search" — use ONLY when the user explicitly wants a LIST of links/results
  to browse ("search for…", "find pages about…", "give me links"). Never for a
  plain question that wants an answer.
- "google_workflow_agent"  — multi-step Google Workspace tasks (Gmail, Calendar, Drive, Docs, Sheets, Slides)
- "spotify_workflow_agent" — multi-step Spotify tasks (play, queue, recommend, curate)
- "google_workspace_assistant" — single-step Workspace lookups or simple drafts
- "spotify_assistant"          — single-step Spotify actions (play a search, pause, volume)
- Any raw tool name from this list, spelled EXACTLY as written:
%s

Rules:
-1. Play/pause/resume/stop/skip/volume of whatever is CURRENTLY playing (a
   movie, a video, music on screen) is "media_control" — a single Windows media
   key, no vision needed. NEVER use find_and_click / a vision tool just to press
   play or pause. Reserve find_and_click for locating a specific on-screen
   element that has no keyboard shortcut.
0. NEVER invent an agent or tool name. If a request is a general question or
   explanation ("explain X", "what is Y", "how do I…", "write me…"), route the
   WHOLE thing to a single "answer" sub-goal — do NOT send it to a Google or
   Spotify agent. Only use google_*/spotify_* when the request clearly needs
   that app or the user's data there.
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
	// Give the model the ACTUAL registry names. The prompt used to say "any raw
	// tool name listed in the tool registry" without ever listing it, so the
	// model guessed plausible-sounding names — "open_application" instead of
	// "open_app" — and the plan died at execution with "tool not found in
	// registry", which the trust layer reports as a hard stop with no recovery.
	prompt := fmt.Sprintf(decompositionPrompt, o.toolNameList(), sysContext, userText)
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

// toolNameList renders the registry's tool names for the decomposition prompt.
// Returns an empty string when no registry is reachable — the prompt still
// works, it just falls back to the named specialist agents.
func (o *Orchestrator) toolNameList() string {
	if o.Executor == nil || o.Executor.Registry == nil {
		return "  (none available)"
	}
	names := o.Executor.Registry.ToolNames()
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString("  - ")
		b.WriteString(n)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
