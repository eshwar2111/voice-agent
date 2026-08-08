package ui

import "github.com/yourname/voice-agent/internal/island"

// UpdateActivity pushes or refreshes a live activity in the island.
// Unknown ids are dropped by the JS registry with a log line, never thrown.
func UpdateActivity(id string, data any) {
	if bridge == nil {
		return
	}
	bridge.Push("activity:update", map[string]any{"id": id, "data": data})
}

// EndActivity removes a live activity.
func EndActivity(id string) {
	if bridge == nil {
		return
	}
	bridge.Push("activity:end", map[string]string{"id": id})
}

// PublishActivities replaces the set of provider-driven activities in the
// island. It is the Publish func injected into island.Registry.
//
// This does NOT disturb push-driven activities (agent.job, trust.approval,
// ambient.nudge) — the JS side keeps the two sets separate and merges them for
// display, so a provider snapshot can never clear a pending approval.
func PublishActivities(as []island.Activity) {
	if bridge == nil {
		return
	}
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		out = append(out, map[string]any{
			"id":          a.ID,
			"kind":        a.Kind,
			"priority":    a.Priority,
			"data":        a.Data,
			"significant": a.Significant,
		})
	}
	bridge.Push("activity:sync", map[string]any{"activities": out})
}
