package tools

// plan_validator.go — Phase 2: Strict Plan Validation & Capabilities Contracts
//
// This package provides deterministic validation of LLM-generated workflow plans
// *before* any external API calls are made. It enforces three tiers of checks:
//
//  1. Whitelist enforcement — every action name must be in the allowed set
//  2. Bounds checking      — max step count, non-empty detail fields
//  3. Dependency pre-flight— simulate artifact availability at each step;
//     if a step consumes an artifact (e.g. "document_id") that no prior step
//     produces, the plan is rejected with a clear, actionable error message.
//
// Ambiguity warnings are emitted (not errors) when a consuming step will
// receive an array-valued artifact (e.g. gmail_list returns many messages) and
// the runtime will auto-pick item[0]. These warnings are surfaced to the user
// in the workflow log so they can narrow their query.

import (
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Issue types
// ─────────────────────────────────────────────────────────────────────────────

// IssueSeverity distinguishes hard plan errors from advisory warnings.
type IssueSeverity string

const (
	SeverityError   IssueSeverity = "error"
	SeverityWarning IssueSeverity = "warning"
)

// PlanIssue represents a single validation finding attached to a plan step.
type PlanIssue struct {
	Step     int
	Action   string
	Severity IssueSeverity
	Message  string
}

// PlanValidation aggregates all issues found for a plan.
type PlanValidation struct {
	Valid  bool // false if any SeverityError issues exist
	Issues []PlanIssue
}

// Errors returns only the fatal issues that prevent execution.
func (v PlanValidation) Errors() []PlanIssue {
	var out []PlanIssue
	for _, i := range v.Issues {
		if i.Severity == SeverityError {
			out = append(out, i)
		}
	}
	return out
}

// Warnings returns only advisory issues.
func (v PlanValidation) Warnings() []PlanIssue {
	var out []PlanIssue
	for _, i := range v.Issues {
		if i.Severity == SeverityWarning {
			out = append(out, i)
		}
	}
	return out
}

// Summary returns a one-line human-readable result for logging.
func (v PlanValidation) ValidationSummary() string {
	errs, warns := len(v.Errors()), len(v.Warnings())
	if v.Valid {
		if warns == 0 {
			return "✅ Plan valid"
		}
		return fmt.Sprintf("✅ Plan valid (%d warning(s))", warns)
	}
	return fmt.Sprintf("❌ Plan invalid: %d error(s), %d warning(s)", errs, warns)
}

// FormatIssues formats all issues as a bulleted log string.
func (v PlanValidation) FormatIssues() string {
	if len(v.Issues) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, issue := range v.Issues {
		icon := "⚠️"
		if issue.Severity == SeverityError {
			icon = "❌"
		}
		if issue.Step > 0 {
			sb.WriteString(fmt.Sprintf("  %s Step %d [%s]: %s\n", icon, issue.Step, issue.Action, issue.Message))
		} else {
			sb.WriteString(fmt.Sprintf("  %s %s\n", icon, issue.Message))
		}
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Google Workspace — artifact capability tables
// ─────────────────────────────────────────────────────────────────────────────

// googleProducers maps action → the artifact key it makes available to later steps.
// The value is the canonical artifact field name used in ToolResult.Artifacts.
var googleProducers = map[string]string{
	"gmail_list":        "gmail_message_id", // array
	"gmail_search":      "gmail_message_id", // array
	"gmail_read":        "gmail_body",
	"gmail_send":        "gmail_sent",
	"docs_create":       "document_id",
	"docs_search":       "document_id", // array — first result used
	"docs_read":         "document_content",
	"sheets_create":     "spreadsheet_id",
	"sheets_write":      "spreadsheet_id",
	"drive_search":      "drive_file_id", // array
	"calendar_list":     "event_id",      // array
	"calendar_add":      "event_id",
	"ai_draft":          "document_id",
	"ai_compose_email":  "gmail_draft",
	"ai_summarise":      "summary_text",
	"ai_analyse":        "analysis_text",
	"slides_create":     "presentation_id",
}

// googleConsumers maps action → the artifact key it REQUIRES from a prior step.
// An empty string means the action has no required upstream artifact.
var googleConsumers = map[string]string{
	"gmail_read":   "gmail_message_id",  // must have a prior list/search
	"sheets_write": "spreadsheet_id",    // must have a prior create or read
}

// googleMultiResult is the set of actions that return arrays (ambiguity risk).
var googleMultiResult = map[string]bool{
	"gmail_list":   true,
	"gmail_search": true,
	"docs_search":  true,
	"drive_search": true,
	"calendar_list": true,
}

// googleMaxSteps is the hard cap for Google workflow plans.
const googleMaxSteps = 8

// ValidateGooglePlan runs all Phase 2 validation checks on a Google workflow plan.
func ValidateGooglePlan(plan *workflowPlan) PlanValidation {
	result := PlanValidation{Valid: true}

	// ── Structural bounds ────────────────────────────────────────────────────
	if len(plan.Steps) == 0 {
		result.Valid = false
		result.Issues = append(result.Issues, PlanIssue{
			Severity: SeverityError,
			Message:  "Plan has no steps",
		})
		return result
	}
	if len(plan.Steps) > googleMaxSteps {
		result.Valid = false
		result.Issues = append(result.Issues, PlanIssue{
			Severity: SeverityError,
			Message:  fmt.Sprintf("Plan has %d steps; maximum is %d", len(plan.Steps), googleMaxSteps),
		})
	}

	// ── Per-step simulation ──────────────────────────────────────────────────
	// available tracks artifact keys produced by executed steps.
	// multiAvailable tracks keys that came from array-returning actions.
	available := make(map[string]bool)
	multiAvailable := make(map[string]bool)

	for _, step := range plan.Steps {
		// 1. Whitelist enforcement
		if !stringInSlice(step.Action, allowedActions) {
			result.Valid = false
			result.Issues = append(result.Issues, PlanIssue{
				Step:     step.Step,
				Action:   step.Action,
				Severity: SeverityError,
				Message:  fmt.Sprintf("Unknown action '%s'. Allowed: %s", step.Action, strings.Join(allowedActions, ", ")),
			})
			continue // can't dependency-check an unknown action
		}

		// 2. Empty detail advisory
		if strings.TrimSpace(step.Detail) == "" {
			result.Issues = append(result.Issues, PlanIssue{
				Step:     step.Step,
				Action:   step.Action,
				Severity: SeverityWarning,
				Message:  "Step detail is empty — execution will use defaults or may fail",
			})
		}

		// 3. Consumer dependency pre-flight
		if requiredKey, needs := googleConsumers[step.Action]; needs && requiredKey != "" {
			if !available[requiredKey] {
				result.Valid = false
				result.Issues = append(result.Issues, PlanIssue{
					Step:     step.Step,
					Action:   step.Action,
					Severity: SeverityError,
					Message:  fmt.Sprintf("Requires artifact '%s' but no prior step produces it. Add a '%s' step before this one.", requiredKey, producerFor(requiredKey, googleProducers)),
				})
			} else if multiAvailable[requiredKey] {
				// Artifact exists but comes from an array — auto-pick warning
				result.Issues = append(result.Issues, PlanIssue{
					Step:     step.Step,
					Action:   step.Action,
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("Artifact '%s' comes from a multi-result step — the first match will be used automatically. Narrow your query to avoid ambiguity.", requiredKey),
				})
			}
		}

		// 4. Register newly produced artifact
		if key, produces := googleProducers[step.Action]; produces && key != "" {
			available[key] = true
			if googleMultiResult[step.Action] {
				multiAvailable[key] = true
			} else {
				// Single-result step disambiguates the artifact
				delete(multiAvailable, key)
			}
		}
	}

	// Final: if any error was added after initial check, ensure Valid=false
	for _, i := range result.Issues {
		if i.Severity == SeverityError {
			result.Valid = false
			break
		}
	}

	return result
}

// ─────────────────────────────────────────────────────────────────────────────
// Spotify — artifact capability tables
// ─────────────────────────────────────────────────────────────────────────────

var spotifyProducers = map[string]string{
	"playlist_create":  "playlist_id",
	"ai_curate":        "playlist_id",
	"playlist_list":    "playlist_id", // array
	"search":           "track_uri",   // array
	"ai_recommend":     "track_uri",   // array
	"now_playing":      "track_uri",
	"recent_tracks":    "track_uri",   // array
	"top_tracks":       "track_uri",   // array
	"queue_add":        "queued",
	"ai_summarise":     "summary_text",
}

var spotifyConsumers = map[string]string{
	"playlist_add_tracks": "playlist_id",
	"playlist_rename":     "playlist_id",
	"ai_remix_analysis":   "playlist_id",
}

var spotifyMultiResult = map[string]bool{
	"playlist_list": true,
	"search":        true,
	"ai_recommend":  true,
	"recent_tracks": true,
	"top_tracks":    true,
}

const spotifyMaxSteps = 6

// ValidateSpotifyPlan runs all Phase 2 validation checks on a Spotify workflow plan.
func ValidateSpotifyPlan(plan *spotifyPlan) PlanValidation {
	result := PlanValidation{Valid: true}

	if len(plan.Steps) == 0 {
		result.Valid = false
		result.Issues = append(result.Issues, PlanIssue{
			Severity: SeverityError,
			Message:  "Plan has no steps",
		})
		return result
	}
	if len(plan.Steps) > spotifyMaxSteps {
		result.Valid = false
		result.Issues = append(result.Issues, PlanIssue{
			Severity: SeverityError,
			Message:  fmt.Sprintf("Plan has %d steps; maximum is %d", len(plan.Steps), spotifyMaxSteps),
		})
	}

	available := make(map[string]bool)
	multiAvailable := make(map[string]bool)

	for _, step := range plan.Steps {
		// 1. Whitelist
		if !stringInSlice(step.Action, spotifyAllowedActions) {
			result.Valid = false
			result.Issues = append(result.Issues, PlanIssue{
				Step:     step.Step,
				Action:   step.Action,
				Severity: SeverityError,
				Message:  fmt.Sprintf("Unknown action '%s'. Allowed: %s", step.Action, strings.Join(spotifyAllowedActions, ", ")),
			})
			continue
		}

		// 2. Empty detail
		if strings.TrimSpace(step.Detail) == "" {
			result.Issues = append(result.Issues, PlanIssue{
				Step:     step.Step,
				Action:   step.Action,
				Severity: SeverityWarning,
				Message:  "Step has no detail — execution will use defaults or may fail",
			})
		}

		// 3. Consumer dependency
		if requiredKey, needs := spotifyConsumers[step.Action]; needs && requiredKey != "" {
			if !available[requiredKey] {
				result.Valid = false
				result.Issues = append(result.Issues, PlanIssue{
					Step:     step.Step,
					Action:   step.Action,
					Severity: SeverityError,
					Message:  fmt.Sprintf("Requires artifact '%s' but no prior step produces it. Add a 'playlist_create' or 'ai_curate' step before this one.", requiredKey),
				})
			} else if multiAvailable[requiredKey] {
				result.Issues = append(result.Issues, PlanIssue{
					Step:     step.Step,
					Action:   step.Action,
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("Artifact '%s' is from a multi-result step — first match will be used. Narrow your query for precision.", requiredKey),
				})
			}
		}

		// 4. Track produced artifacts
		if key, produces := spotifyProducers[step.Action]; produces && key != "" {
			available[key] = true
			if spotifyMultiResult[step.Action] {
				multiAvailable[key] = true
			} else {
				delete(multiAvailable, key)
			}
		}
	}

	for _, i := range result.Issues {
		if i.Severity == SeverityError {
			result.Valid = false
			break
		}
	}

	return result
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// stringInSlice checks if s exists in the allowed slice.
func stringInSlice(s string, slice []string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// producerFor finds which action is the primary producer of a given artifact key.
// Used for generating helpful error messages.
func producerFor(key string, producers map[string]string) string {
	for action, produced := range producers {
		if produced == key {
			return action
		}
	}
	return "a compatible step"
}
