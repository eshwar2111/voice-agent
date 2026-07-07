package tools

import "encoding/json"

// ToolResult represents a structured output from any core primitive tool.
// Tools should marshal this structured object into their return so that downstream consumers
// (like the Workflow Agents) can extract machine-readable artifacts instead of fragile regex parsing.
type ToolResult struct {
	Summary   string                 `json:"summary"`
	Artifacts map[string]interface{} `json:"artifacts,omitempty"`
}

// String safely serializes a ToolResult into a JSON string.
// If serialization fails, it falls back to returning the raw Summary.
func (tr ToolResult) String() string {
	b, err := json.Marshal(tr)
	if err != nil {
		return tr.Summary
	}
	return string(b)
}

// ParseToolResult safely unpacks a raw string into a ToolResult.
// If the string is not valid JSON (e.g., legacy tool output), it places the entire
// raw string into the Summary field so upstream tools don't break.
func ParseToolResult(raw string) ToolResult {
	var tr ToolResult
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		return ToolResult{Summary: raw}
	}
	return tr
}

// ─────────────────────────────────────────────────────────────────────────────
// Ambiguity helpers
// ─────────────────────────────────────────────────────────────────────────────

// AmbiguousChoice represents a single option in an ambiguous result.
type AmbiguousChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AmbiguousResult builds a ToolResult that signals the caller that multiple
// items were found and a specific one needs to be selected before proceeding.
// The workflow agent will detect this sentinel and surface the choices to
// the user rather than auto-picking item[0].
//
// Example:
//   return AmbiguousResult("Found 3 matching docs — which did you mean?", choices)
func AmbiguousResult(message string, choices []AmbiguousChoice) ToolResult {
	raw := make([]interface{}, len(choices))
	for i, c := range choices {
		raw[i] = map[string]string{"id": c.ID, "label": c.Label}
	}
	return ToolResult{
		Summary: message,
		Artifacts: map[string]interface{}{
			"type":    "ambiguous",
			"choices": raw,
		},
	}
}

// IsAmbiguous returns true if this ToolResult represents an ambiguity signal.
func IsAmbiguous(tr ToolResult) bool {
	if tr.Artifacts == nil {
		return false
	}
	t, _ := tr.Artifacts["type"].(string)
	return t == "ambiguous"
}

// AmbiguousChoices extracts the choices from an ambiguous ToolResult.
func AmbiguousChoices(tr ToolResult) []AmbiguousChoice {
	raw, ok := tr.Artifacts["choices"].([]interface{})
	if !ok {
		return nil
	}
	var out []AmbiguousChoice
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		label, _ := m["label"].(string)
		out = append(out, AmbiguousChoice{ID: id, Label: label})
	}
	return out
}

// PlanApprovalResult signals to the executor that a multi-step plan has been
// generated and needs user approval before proceeding.
func PlanApprovalResult(title, summary string, plan interface{}) ToolResult {
	return ToolResult{
		Summary: summary,
		Artifacts: map[string]interface{}{
			"type":  "workflow_approval",
			"title": title,
			"plan":  plan,
		},
	}
}


