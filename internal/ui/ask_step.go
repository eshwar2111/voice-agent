package ui

import "encoding/json"

// AskStepChoice shows a Retry/Stop card for a failed step. Returns true=Retry.
func AskStepChoice(reason string) bool {
	card, _ := json.Marshal(map[string]interface{}{
		"type": "workflow_approval", "title": "Step failed",
		"plan": map[string]interface{}{"goal": reason,
			"steps": []map[string]string{{"label": "Choose", "value": "Approve = Retry · Cancel = Stop"}}},
	})
	return RequestConfirmationCard(string(card))
}
