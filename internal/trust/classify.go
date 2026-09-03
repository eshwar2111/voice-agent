package trust

import "encoding/json"

// riskyTools require human approval. Deliberately NARROW: per product intent,
// only genuinely destructive or outbound-irreversible actions gate — deleting
// files, sending messages/email, and executing arbitrary code. Opening, reading,
// searching, creating, writing, launching apps, and UI automation do NOT gate;
// they are recoverable and asking on every one is friction, not safety.
var riskyTools = map[string]bool{
	// Destructive to the user's files
	"delete_file": true,
	"move_file":   true, // can overwrite / relocate out from under the user
	// Outbound / sends a message the user can't unsend
	"gmail_send": true,
	"google_ai":  true, // composes AND sends email/docs on the user's behalf
	// Arbitrary code execution — more dangerous than any of the above
	"run_python":   true,
	"run_terminal": true,
}

// dangerousActions bump a control tool to Risky based on params — closing/killing
// or ending the session are the only control actions worth an approval.
var dangerousActions = map[string]bool{
	"shutdown": true, "restart": true, "logoff": true,
}

type RiskClassifier struct{}

func NewRiskClassifier() *RiskClassifier { return &RiskClassifier{} }

func (c *RiskClassifier) Classify(tool string, params json.RawMessage) Risk {
	if riskyTools[tool] {
		return Risky
	}
	// Param-aware bump: only system_control shutdown/restart/logoff gate.
	if tool == "system_control" {
		var p struct {
			Action string `json:"action"`
		}
		if len(params) > 0 && json.Unmarshal(params, &p) == nil && dangerousActions[p.Action] {
			return Risky
		}
		return Safe
	}
	// Everything else — open/read/search/create/write/launch/automate — is Safe.
	// The workflow agents keep their own inner approval card for the specific
	// sub-steps that send/modify; this outer gate stays out of the way.
	return Safe
}
