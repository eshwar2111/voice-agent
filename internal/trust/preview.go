package trust

import (
	"encoding/json"
	"fmt"
)

func ShouldGate(steps []Step, risks []Risk) bool {
	if len(steps) >= 2 {
		return true
	}
	for _, r := range risks {
		if r == Risky {
			return true
		}
	}
	return false
}

func BuildPreview(command string, steps []Step, risks []Risk, describe func(tool string, params json.RawMessage) string) string {
	type cardStep struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	card := map[string]interface{}{
		"type":  "workflow_approval",
		"title": fmt.Sprintf("Review this %d-step task", len(steps)),
	}
	if len(steps) == 1 {
		card["title"] = "Review action request"
	}
	out := make([]cardStep, len(steps))
	for i, s := range steps {
		tag := "Safe"
		if i < len(risks) && risks[i] == Risky {
			tag = "⚠ Risky"
		}
		val := ""
		if describe != nil {
			val = describe(s.Tool, s.Params)
		}
		if val == "" {
			val = s.Goal
		}
		if val == "" {
			val = s.Tool
		}
		out[i] = cardStep{Label: fmt.Sprintf("Step %d · %s", i+1, tag), Value: val}
	}
	card["plan"] = map[string]interface{}{"goal": command, "steps": out}
	b, _ := json.Marshal(card)
	return string(b)
}
