package trust

import (
	"context"
	"encoding/json"
	"os"
	"strings"
)

var fuzzyTools = map[string]bool{
	"native_click": true, "keyboard_type": true, "keyboard_combo": true,
	"mouse_click": true, "mouse_move": true, "mouse_drag": true,
}

type StepVerifier struct {
	// LLMJudge returns (ok, reason). May be nil.
	LLMJudge func(ctx context.Context, goal, observation string) (bool, string)
}

func NewStepVerifier(judge func(ctx context.Context, goal, observation string) (bool, string)) *StepVerifier {
	return &StepVerifier{LLMJudge: judge}
}

func pathParam(params json.RawMessage) string {
	var p struct{ Path string `json:"path"` }
	if len(params) > 0 && json.Unmarshal(params, &p) == nil {
		return p.Path
	}
	return ""
}

func (v *StepVerifier) Verify(ctx context.Context, step Step, result string, execErr error) (bool, string) {
	// 1. Tool error (Exec already folded ToolResult failures into execErr).
	if execErr != nil {
		return false, execErr.Error()
	}
	// 2. Deterministic post-conditions.
	switch step.Tool {
	case "create_file", "write_file":
		if path := pathParam(step.Params); path != "" {
			if _, err := os.Stat(path); err != nil {
				return false, "expected file was not created: " + path
			}
			return true, ""
		}
	case "delete_file":
		if path := pathParam(step.Params); path != "" {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return true, ""
			}
			return false, "file still exists after delete: " + path
		}
	case "get_datetime", "read_file", "search":
		if strings.TrimSpace(result) == "" {
			return false, "empty result"
		}
		return true, ""
	}
	// 3. Fuzzy GUI/vision → LLM judge if available; else trust.
	if fuzzyTools[step.Tool] {
		if v.LLMJudge == nil {
			return true, ""
		}
		return v.LLMJudge(ctx, step.Goal, result)
	}
	// 4. Default: trust the tool.
	return true, ""
}
