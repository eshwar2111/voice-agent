package trust

import "encoding/json"

// riskyTools are always Risky by name.
var riskyTools = map[string]bool{
	"delete_file": true, "write_file": true, "create_file": true,
	"keyboard_type": true, "keyboard_combo": true,
	"native_click": true, "mouse_click": true, "mouse_move": true, "mouse_drag": true,
	"run_python": true, "run_terminal": true, "browser_navigate": true,
	"google_workflow_agent": true, "spotify_workflow_agent": true, "google_ai": true,
}

// safeTools are always Safe by name.
var safeTools = map[string]bool{
	"get_datetime": true, "list_files": true, "read_file": true, "search": true,
	"screenshot_analysis": true, "explain_selection": true, "recall": true,
	"list_memories": true, "browser_read_page": true,
}

// dangerousActions bump a normally-benign control tool to Risky based on params.
var dangerousActions = map[string]bool{
	"shutdown": true, "restart": true, "logoff": true, "close": true, "kill": true,
}

type RiskClassifier struct{}

func NewRiskClassifier() *RiskClassifier { return &RiskClassifier{} }

func (c *RiskClassifier) Classify(tool string, params json.RawMessage) Risk {
	if riskyTools[tool] {
		return Risky
	}
	// Param-aware bump for control tools (media/window/system_control).
	if tool == "system_control" || tool == "window_control" || tool == "media_control" {
		var p struct{ Action string `json:"action"` }
		if len(params) > 0 && json.Unmarshal(params, &p) == nil && dangerousActions[p.Action] {
			return Risky
		}
		return Safe
	}
	if safeTools[tool] {
		return Safe
	}
	return Risky // unknown → safe default is to gate
}
