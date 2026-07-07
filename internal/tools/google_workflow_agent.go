package tools

// GoogleWorkflowAgent — orchestrates multi-step, cross-product Google Workspace
// workflows.  Unlike GoogleAIAssistantTool (single action) and
// GoogleWorkspaceAssistantTool (classification→dispatch), this agent accepts a
// high-level natural-language goal, breaks it into an ordered plan, executes each
// step sequentially, passes results forward as context, and returns a final
// consolidated summary of every action taken.
//
// Example workflows it handles:
//   "Read the Q1 sales sheet, summarise it, draft slides from the summary, then
//    email them to my team."
//   "Find my latest project doc, analyse it, create a task tracker spreadsheet."
//   "Check my inbox for unread budget emails, extract the numbers, add them to
//    the finance sheet and send a confirmation."

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/auth"
	"github.com/yourname/voice-agent/internal/llm"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tool definition
// ─────────────────────────────────────────────────────────────────────────────

type GoogleWorkflowAgentTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *GoogleWorkflowAgentTool) Name() string { return "google_workflow_agent" }

func (t *GoogleWorkflowAgentTool) Description() string {
	return "Advanced multi-step Google Workspace workflow agent. Chains Gmail, Drive, Docs, Sheets, Slides and Calendar operations in sequence so you can say things like: 'Read the Q1 sales sheet, summarise it, draft slides from the summary, then email the link to my team.' Each step's output is passed as context into the next step."
}

func (t *GoogleWorkflowAgentTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"goal": {"type": "string", "description": "High-level natural-language workflow goal spanning multiple Google Workspace products."},
			"context": {"type": "string", "description": "Optional extra context (e.g., recipient name, sheet ID, date range)."},
			"approved_plan": {"type": "string", "description": "JSON string of a plan generated in a previous dry-run. If provided, execution bypasses planning and runs these steps directly."}
		},
		"required": ["goal"]
	}`
}

func (t *GoogleWorkflowAgentTool) RequiresConfirmation() bool { return false }

// ─────────────────────────────────────────────────────────────────────────────
// Step plan types
// ─────────────────────────────────────────────────────────────────────────────

type workflowStep struct {
	Step   int    `json:"step"`
	Action string `json:"action"`
	Detail string `json:"detail"`
}

type workflowPlan struct {
	Steps   []workflowStep `json:"steps"`
	Summary string         `json:"summary"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Execute
// ─────────────────────────────────────────────────────────────────────────────

func (t *GoogleWorkflowAgentTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Goal         string `json:"goal"`
		Context      string `json:"context"`
		ApprovedPlan string `json:"approved_plan"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Goal) == "" {
		return "", fmt.Errorf("goal is required")
	}

	var plan *workflowPlan
	var err error

	if args.ApprovedPlan != "" {
		// Use the plan that the user just reviewed and approved
		plan = &workflowPlan{}
		if err := json.Unmarshal([]byte(args.ApprovedPlan), plan); err != nil {
			return "", fmt.Errorf("failed to parse approved plan: %w", err)
		}
	} else {
		// Step 1: Generate Plan
		plan, err = t.plan(ctx, args.Goal, args.Context)
		if err != nil {
			return "", fmt.Errorf("planning failed: %w", err)
		}

		// ── Phase 2: Validate plan ──────────────────────────────────────────
		validation := ValidateGooglePlan(plan)
		if !validation.Valid {
			summary := fmt.Sprintf("Workflow plan is invalid and cannot be executed.\n\n%s\n\nPlease rephrase your goal to address the issues above.", validation.FormatIssues())
			return ToolResult{
				Summary: summary,
				Artifacts: map[string]interface{}{
					"goal":       args.Goal,
					"validation": validation.Issues,
					"status":     "plan_rejected",
				},
			}.String(), nil
		}

		// ── Phase 3: Trigger Approval Boundary ──────────────────────────────
		// Return the plan to the executor so it can prompt the user
		var steps []map[string]string
		for _, s := range plan.Steps {
			steps = append(steps, map[string]string{
				"label": fmt.Sprintf("Step %d: %s", s.Step, s.Action),
				"value": s.Detail,
			})
		}

		return PlanApprovalResult(
			"Review Google Workflow",
			fmt.Sprintf("I've drafted a %d-step plan to accomplish: %s", len(plan.Steps), args.Goal),
			map[string]interface{}{
				"goal":   args.Goal,
				"steps":  steps,
				"source": "google_workflow_agent",
				"raw":    plan, // This will be passed back in approved_plan
			},
		).String(), nil
	}
	// ── Execution Loop ───────────────────────────────────────────────────────
	var log strings.Builder
	log.WriteString(fmt.Sprintf("Executing Google Workflow: %s\n\n", args.Goal))

	carryJSON := args.Context
	for _, step := range plan.Steps {
		rawResult, err := t.executeStep(ctx, step, carryJSON)
		if err != nil {
			log.WriteString(fmt.Sprintf("Step %d [%s]: ERROR — %v\n\n", step.Step, step.Action, err))
			// continue to remaining steps; partial failure is acceptable
		} else {
			// Parse the structured result so we log only the human-readable Summary
			parsed := ParseToolResult(rawResult)
			log.WriteString(fmt.Sprintf("Step %d [%s]: %s\n\n", step.Step, step.Action, step.Detail))

			// Check for ambiguity (Phase 2)
			if IsAmbiguous(parsed) {
				log.WriteString(fmt.Sprintf("⚠️ AMBIGUITY DETECTED: %s\n", parsed.Summary))
				log.WriteString("Halting workflow for user clarification.\n")
				return ToolResult{
					Summary:   parsed.Summary,
					Artifacts: parsed.Artifacts,
				}.String(), nil
			}

			summary := parsed.Summary
			if len(summary) > 800 {
				summary = summary[:800] + "… [truncated]"
			}
			log.WriteString(fmt.Sprintf("Result: %s\n\n", summary))
			// Pass the raw JSON string forward so the next step can extract typed fields
			carryJSON = rawResult
		}
	}

	// Step 3: Final synthesis
	synthesis, _ := t.synthesise(ctx, args.Goal, log.String())
	log.WriteString("\n─────────────────────────────────────\n")
	log.WriteString("Workflow complete.\n\n")
	log.WriteString(synthesis)

	return ToolResult{
		Summary: synthesis,
		Artifacts: map[string]interface{}{
			"goal": args.Goal,
			"log":  log.String(),
		},
	}.String(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// plan — ask the LLM to decompose the goal into ordered steps
// ─────────────────────────────────────────────────────────────────────────────

var allowedActions = []string{
	"gmail_list", "gmail_search", "gmail_read", "gmail_send",
	"calendar_list", "calendar_add",
	"drive_search",
	"docs_read", "docs_create", "docs_search",
	"sheets_read", "sheets_write", "sheets_create",
	"slides_create",
	"ai_summarise", "ai_compose_email", "ai_analyse", "ai_draft",
}

func (t *GoogleWorkflowAgentTool) plan(ctx context.Context, goal, extra string) (*workflowPlan, error) {
	prompt := fmt.Sprintf(`You are a Google Workspace workflow planner.

GOAL: %s
CONTEXT: %s

Break this into an ordered list of concrete steps. Each step must use exactly one of these actions:
%s

Return ONLY valid JSON matching this schema:
{"steps": [{"step": 1, "action": "action_name", "detail": "what to do in plain English"}], "summary": "one-line summary"}

Rules:
- Maximum 8 steps
- Each step must depend on something prior OR be independent
- Prefer ai_summarise to condense between data-fetch and create steps
- Only include steps that are truly needed`, goal, extra, strings.Join(allowedActions, ", "))

	resp, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return nil, err
	}

	var plan workflowPlan
	if err := json.Unmarshal([]byte(extractJSONText(resp)), &plan); err != nil {
		// Fallback: single ai_summarise step
		return &workflowPlan{
			Steps:   []workflowStep{{Step: 1, Action: "ai_summarise", Detail: goal}},
			Summary: goal,
		}, nil
	}
	return &plan, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// executeStep — dispatch each planned step to the right primitive
// ─────────────────────────────────────────────────────────────────────────────

func (t *GoogleWorkflowAgentTool) executeStep(ctx context.Context, step workflowStep, carry string) (string, error) {
	// Parse structured artifact from the previous step's raw output
	carryResult := ParseToolResult(carry)

	switch step.Action {

	// ── Gmail ────────────────────────────────────────────────────────────────
	case "gmail_list":
		return (&GoogleGmailListTool{Cfg: t.Cfg}).Execute(ctx,
			mustJSON(map[string]any{"max_results": 5, "unread_only": true}))

	case "gmail_search":
		return (&GoogleGmailSearchTool{Cfg: t.Cfg}).Execute(ctx,
			mustJSON(map[string]any{"query": step.Detail, "max_results": 5}))

	case "gmail_read":
		// Try typed artifact first, then fall back to string scan
		msgID := extractArtifactString(carryResult, "id")
		if msgID == "" {
			msgID = extractArtifactString(carryResult, "message_id")
		}
		if msgID == "" {
			msgID = extractFieldFromContext(carry, "id")
		}
		if msgID == "" {
			return "No message ID available in context for gmail_read step.", nil
		}
		return (&GoogleGmailGetEmailTool{Cfg: t.Cfg}).Execute(ctx,
			mustJSON(map[string]any{"messageId": msgID}))

	case "gmail_send":
		return t.aiSendEmail(ctx, step.Detail, carryResult.Summary)

	// ── Calendar ─────────────────────────────────────────────────────────────
	case "calendar_list":
		return (&GoogleCalendarListTool{Cfg: t.Cfg}).Execute(ctx,
			mustJSON(map[string]any{"max_results": 5}))

	case "calendar_add":
		return t.aiAddCalendarEvent(ctx, step.Detail, carryResult.Summary)

	// ── Drive ─────────────────────────────────────────────────────────────────
	case "drive_search":
		return (&GoogleDriveListTool{Cfg: t.Cfg}).Execute(ctx,
			mustJSON(map[string]any{"query": step.Detail, "maxResults": 5}))

	// ── Docs ──────────────────────────────────────────────────────────────────
	case "docs_read":
		// Try typed artifact for document_id, fall back to string scan
		docID := extractArtifactString(carryResult, "document_id")
		if docID == "" {
			docID = extractFieldFromContext(carry, "document_id")
		}
		name := step.Detail
		return (&GoogleDocsReadTool{Cfg: t.Cfg}).Execute(ctx,
			mustJSON(map[string]any{"document_id": docID, "name": name}))

	case "docs_create":
		return t.aiCreateDoc(ctx, step.Detail, carryResult.Summary)

	case "docs_search":
		return (&GoogleDocsSearchTool{Cfg: t.Cfg}).Execute(ctx,
			mustJSON(map[string]any{"query": step.Detail, "max_results": 5}))

	// ── Sheets ───────────────────────────────────────────────────────────────
	case "sheets_read":
		// Try typed artifact for spreadsheet_id, fall back to string scan
		sheetID := extractArtifactString(carryResult, "spreadsheet_id")
		if sheetID == "" {
			sheetID = extractFieldFromContext(carry, "spreadsheet_id")
		}
		name := step.Detail
		return (&GoogleSheetsReadTool{Cfg: t.Cfg}).Execute(ctx,
			mustJSON(map[string]any{"spreadsheet_id": sheetID, "name": name}))

	case "sheets_write":
		return t.aiWriteSheet(ctx, step.Detail, carryResult.Summary)

	case "sheets_create":
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		client, err := auth.GetGoogleClient(ctx, t.Cfg)
		if err != nil {
			return "", err
		}
		return ai.createSheetReport(ctx, client, step.Detail+"\n\n"+carryResult.Summary, nil)

	// ── Slides ───────────────────────────────────────────────────────────────
	case "slides_create":
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		return ai.draftPresentation(ctx, step.Detail+"\n\n"+carryResult.Summary)

	// ── AI operations ────────────────────────────────────────────────────────
	case "ai_summarise":
		return t.aiSummarise(ctx, step.Detail, carryResult.Summary)

	case "ai_compose_email":
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		return ai.composeEmail(ctx, step.Detail+"\nBackground:\n"+carryResult.Summary, nil)

	case "ai_analyse":
		return t.aiAnalyse(ctx, step.Detail, carryResult.Summary)

	case "ai_draft":
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		return ai.createDoc(ctx, nil, step.Detail+"\nSource material:\n"+carryResult.Summary, nil)

	default:
		return fmt.Sprintf("Unknown action '%s' — skipping.", step.Action), nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AI helpers used by workflow steps
// ─────────────────────────────────────────────────────────────────────────────

func (t *GoogleWorkflowAgentTool) aiSummarise(ctx context.Context, task, data string) (string, error) {
	prompt := fmt.Sprintf(`Summarise the following data concisely for use in the next workflow step.

TASK CONTEXT: %s

DATA:
%s

Provide a concise, structured summary. Focus on actionable facts and numbers.`, task, data)
	return t.Provider.Generate(ctx, prompt, nil)
}

func (t *GoogleWorkflowAgentTool) aiAnalyse(ctx context.Context, task, data string) (string, error) {
	prompt := fmt.Sprintf(`You are a data analyst. Analyse the following data:

TASK: %s

DATA:
%s

Provide:
1. Key insights
2. Trends or patterns
3. Anomalies
4. Recommended next action`, task, data)
	return t.Provider.Generate(ctx, prompt, nil)
}

func (t *GoogleWorkflowAgentTool) aiSendEmail(ctx context.Context, detail, carry string) (string, error) {
	// Use GoogleGmailSendTool — extract recipient from detail/carry
	prompt := fmt.Sprintf(`Extract the email recipient and compose a professional email.

TASK: %s
CONTEXT: %s

Return ONLY JSON: {"to": "email@example.com", "subject": "Subject", "body": "Body text"}`, detail, carry)
	resp, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	var email struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(extractJSONText(resp)), &email); err != nil {
		return fmt.Sprintf("Email draft ready (no auto-send — couldn't parse recipient):\n%s", resp), nil
	}
	return (&GoogleGmailSendTool{Cfg: t.Cfg}).Execute(ctx,
		mustJSON(map[string]any{
			"to":      email.To,
			"subject": email.Subject,
			"body":    email.Body,
		}))
}

func (t *GoogleWorkflowAgentTool) aiAddCalendarEvent(ctx context.Context, detail, carry string) (string, error) {
	prompt := fmt.Sprintf(`Extract calendar event details from this request.

TASK: %s
CONTEXT: %s

Return ONLY JSON:
{"title": "Event title", "start": "2006-01-02T15:04:05Z", "end": "2006-01-02T16:04:05Z", "description": "Optional description"}

Use RFC3339 dates. If no specific time given, use tomorrow at 10am local time.`, detail, carry)
	resp, err := t.Provider.Generate(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	var ev struct {
		Title       string `json:"title"`
		Start       string `json:"start"`
		End         string `json:"end"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(extractJSONText(resp)), &ev); err != nil {
		return fmt.Sprintf("Could not parse event details: %s", resp), nil
	}
	return (&GoogleCalendarAddEventTool{Cfg: t.Cfg}).Execute(ctx,
		mustJSON(map[string]any{
			"title":       ev.Title,
			"start":       ev.Start,
			"end":         ev.End,
			"description": ev.Description,
		}))
}

func (t *GoogleWorkflowAgentTool) aiCreateDoc(ctx context.Context, detail, carry string) (string, error) {
	ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}
	return ai.createDoc(ctx, client, detail+"\n\nSource material:\n"+carry, nil)
}

func (t *GoogleWorkflowAgentTool) aiWriteSheet(ctx context.Context, detail, carry string) (string, error) {
	// Find sheet ID in carry context; if not found, create a new sheet
	sheetID := extractFieldFromContext(carry, "spreadsheet_id")
	if sheetID != "" {
		ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
		client, err := auth.GetGoogleClient(ctx, t.Cfg)
		if err != nil {
			return "", err
		}
		return ai.smartResearchSheet(ctx, client, detail+"\n\n"+carry,
			map[string]interface{}{"sheet_id": sheetID})
	}
	// No existing sheet — create one
	ai := &GoogleAIAssistantTool{Cfg: t.Cfg, Provider: t.Provider}
	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}
	return ai.createSheetReport(ctx, client, detail+"\n\nData to structure:\n"+carry, nil)
}

// ─────────────────────────────────────────────────────────────────────────────
// Final synthesis
// ─────────────────────────────────────────────────────────────────────────────

func (t *GoogleWorkflowAgentTool) synthesise(ctx context.Context, goal, log string) (string, error) {
	if len(log) > 3000 {
		log = log[len(log)-3000:]
	}
	prompt := fmt.Sprintf(`You are summarising a completed Google Workspace workflow.

GOAL: %s

EXECUTION LOG:
%s

Write a short, professional summary (3-5 sentences) of what was accomplished, what was created/sent, and any items that need user attention.`, goal, log)
	return t.Provider.Generate(ctx, prompt, nil)
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility: workspace brief with richer Gmail preview
// ─────────────────────────────────────────────────────────────────────────────

type GoogleWorkspaceBriefTool struct {
	Cfg      *config.Config
	Provider llm.Provider
}

func (t *GoogleWorkspaceBriefTool) Name() string { return "google_workspace_brief" }
func (t *GoogleWorkspaceBriefTool) Description() string {
	return "Delivers a rich morning/evening Google Workspace brief: summarises unread emails with subjects, lists today's calendar events, shows recent Drive activity, and highlights anything needing urgent attention."
}
func (t *GoogleWorkspaceBriefTool) Parameters() string {
	return `{"type": "object", "properties": {"focus": {"type": "string", "description": "Optional focus: 'inbox', 'calendar', 'drive', or 'all' (default: 'all')"}}, "required": []}`
}
func (t *GoogleWorkspaceBriefTool) RequiresConfirmation() bool { return false }

func (t *GoogleWorkspaceBriefTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Focus string `json:"focus"`
	}
	json.Unmarshal(params, &args)
	if args.Focus == "" {
		args.Focus = "all"
	}

	client, err := auth.GetGoogleClient(ctx, t.Cfg)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("Google Workspace Brief\n")
	sb.WriteString(time.Now().Format("Monday, 02 Jan 2006 15:04\n\n"))

	// ── Gmail ──────────────────────────────────────────────────────────────
	if args.Focus == "all" || args.Focus == "inbox" {
		gmailSvc, err := gmail.NewService(ctx, option.WithHTTPClient(client))
		if err == nil {
			msgs, err := gmailSvc.Users.Messages.List("me").
				Q("is:unread category:primary").MaxResults(8).Do()
			if err == nil && msgs != nil {
				sb.WriteString(fmt.Sprintf("Inbox — %d unread primary\n", len(msgs.Messages)))
				for i, msg := range msgs.Messages {
					if i >= 5 {
						break
					}
					full, err := gmailSvc.Users.Messages.Get("me", msg.Id).
						Format("metadata").
						MetadataHeaders("Subject", "From").Do()
					if err != nil {
						continue
					}
					subject, from := "", ""
					for _, h := range full.Payload.Headers {
						switch h.Name {
						case "Subject":
							subject = h.Value
						case "From":
							from = h.Value
						}
					}
					snippet := full.Snippet
					if len(snippet) > 80 {
						snippet = snippet[:80] + "…"
					}
					sb.WriteString(fmt.Sprintf("  • From: %s\n    Subject: %s\n    Preview: %s\n\n", from, subject, snippet))
				}
			}
		}
	}

	// ── Calendar ──────────────────────────────────────────────────────────
	if args.Focus == "all" || args.Focus == "calendar" {
		calSvc, err := calendar.NewService(ctx, option.WithHTTPClient(client))
		if err == nil {
			now := time.Now()
			endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
			events, err := calSvc.Events.List("primary").
				SingleEvents(true).
				TimeMin(now.Format(time.RFC3339)).
				TimeMax(endOfDay.Format(time.RFC3339)).
				OrderBy("startTime").
				MaxResults(8).Do()
			if err == nil && events != nil {
				sb.WriteString(fmt.Sprintf("Calendar — %d event(s) today\n", len(events.Items)))
				for _, ev := range events.Items {
					when := ev.Start.DateTime
					if when == "" {
						when = ev.Start.Date + " (all day)"
					} else {
						t, err := time.Parse(time.RFC3339, when)
						if err == nil {
							when = t.Format("15:04")
						}
					}
					loc := ""
					if ev.Location != "" {
						loc = " @ " + ev.Location
					}
					sb.WriteString(fmt.Sprintf("  • %s — %s%s\n", when, fallbackText(ev.Summary, "Untitled"), loc))
				}
				sb.WriteString("\n")
			}
		}
	}

	// ── Drive ─────────────────────────────────────────────────────────────
	if args.Focus == "all" || args.Focus == "drive" {
		driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(client))
		if err == nil {
			files, err := driveSvc.Files.List().
				PageSize(5).
				OrderBy("modifiedTime desc").
				Fields("files(id,name,mimeType,modifiedTime,lastModifyingUser)").
				Do()
			if err == nil && files != nil && len(files.Files) > 0 {
				sb.WriteString("Recent Drive Activity\n")
				for _, f := range files.Files {
					label := workspaceMimeLabel(f.MimeType)
					who := ""
					if f.LastModifyingUser != nil {
						who = " by " + f.LastModifyingUser.DisplayName
					}
					sb.WriteString(fmt.Sprintf("  • %s (%s)%s\n", f.Name, label, who))
				}
				sb.WriteString("\n")
			}
		}
	}

	// ── AI triage ─────────────────────────────────────────────────────────
	// Ask the LLM to highlight anything urgent
	rawBrief := sb.String()
	triagePrompt := fmt.Sprintf(`You are an executive assistant. Review this brief and highlight anything that needs urgent attention or action today.

BRIEF:
%s

List up to 3 priority items in bullet form. If nothing is urgent, say "No urgent items — you're clear."`, rawBrief)
	triage, err := t.Provider.Generate(ctx, triagePrompt, nil)
	if err == nil {
		sb.WriteString("Priority Items\n")
		sb.WriteString(triage)
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// extractArtifactString reads a string field from а ParsedToolResult's Artifacts map.
// This replaces fragile regex-based context scanning for typed IDs.
func extractArtifactString(tr ToolResult, field string) string {
	if tr.Artifacts == nil {
		return ""
	}
	if v, ok := tr.Artifacts[field]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	// Also try to dig into nested "data" arrays (e.g., gmail_list, calendar_list)
	if data, ok := tr.Artifacts["data"]; ok {
		switch d := data.(type) {
		case []interface{}:
			if len(d) > 0 {
				if item, ok := d[0].(map[string]interface{}); ok {
					if v, ok := item[field]; ok {
						if s, ok := v.(string); ok {
							return s
						}
					}
				}
			}
		}
	}
	return ""
}

// extractFieldFromContext is a fallback string search for legacy or plain-text carry values.
func extractFieldFromContext(carry, field string) string {
	lower := strings.ToLower(carry)
	target := strings.ToLower(field) + ":"
	idx := strings.Index(lower, target)
	if idx == -1 {
		return ""
	}
	rest := carry[idx+len(target):]
	rest = strings.TrimSpace(rest)
	end := strings.IndexAny(rest, " \n\t\r\"'")
	if end == -1 {
		return rest
	}
	return rest[:end]
}
