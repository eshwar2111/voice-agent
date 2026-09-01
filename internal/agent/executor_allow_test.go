package agent

import (
	gocontext "context"
	"encoding/json"
	"testing"

	"github.com/yourname/voice-agent/internal/tools"
)

// spyTool records whether Execute was ever called so a test can prove the
// Allow gate blocks a tool BEFORE it runs.
type spyTool struct {
	name     string
	executed bool
}

func (t *spyTool) Name() string                 { return t.name }
func (t *spyTool) Description() string           { return "spy" }
func (t *spyTool) Parameters() string            { return `{"type":"object"}` }
func (t *spyTool) RequiresConfirmation() bool     { return false }
func (t *spyTool) Execute(_ gocontext.Context, _ json.RawMessage) (string, error) {
	t.executed = true
	return "ran", nil
}

// TestExecutePlanAllowGateBlocks verifies that on the legacy (Trusted==nil)
// path a tool rejected by the Allow func returns a non-nil error and is never
// executed — the security-profile check the Tier-1 orchestrator path relies on.
func TestExecutePlanAllowGateBlocks(t *testing.T) {
	reg := tools.NewRegistry()
	spy := &spyTool{name: "dangerous_tool"}
	reg.Register(spy)

	e := NewExecutor(reg)
	// Trusted stays nil -> legacy per-task loop.
	e.Allow = func(tool string) bool { return tool != "dangerous_tool" }

	plan := Plan{
		Intent: "test",
		Tasks:  []Task{{Tool: "dangerous_tool", Params: json.RawMessage(`{}`)}},
	}

	err := e.ExecutePlan(gocontext.Background(), plan)
	if err == nil {
		t.Fatal("expected error when Allow rejects the tool, got nil")
	}
	if spy.executed {
		t.Fatal("tool was executed despite being rejected by the Allow gate")
	}
}
