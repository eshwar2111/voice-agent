package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yourname/voice-agent/internal/trust"
)

// TestGraphExecutorHasTrustedField guards that the delegation seam exists.
func TestGraphExecutorHasTrustedField(t *testing.T) {
	e := &GraphExecutor{Trusted: &trust.TrustedExecutor{}}
	if e.Trusted == nil {
		t.Fatal("Trusted field must be settable")
	}
}

// TestExecutePlanDelegatesWhenTrustedSet proves that when Trusted is wired,
// ExecutePlan converts the plan to trust.Steps and delegates to Run rather than
// running the legacy loop. The registry is nil on purpose: because Exec is
// pre-set, the trust path must never touch the registry, so reaching both tasks
// via the injected Exec is proof of delegation.
func TestExecutePlanDelegatesWhenTrustedSet(t *testing.T) {
	var ran []string
	te := &trust.TrustedExecutor{
		Classifier: trust.NewRiskClassifier(),
		Verifier:   trust.NewStepVerifier(nil),
		Recoverer:  trust.NewLadderRecoverer(2),
		Confirm:    func(string) bool { return true },
		Exec: func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
			ran = append(ran, tool)
			return "ok", nil
		},
	}
	e := &GraphExecutor{Registry: nil, Trusted: te}
	plan := Plan{
		Transcript: "do two things",
		Intent:     "test",
		Tasks: []Task{
			{Tool: "search", Params: json.RawMessage(`{}`)},
			{Tool: "get_datetime", Params: json.RawMessage(`{}`)},
		},
	}
	if err := e.ExecutePlan(context.Background(), plan); err != nil {
		t.Fatalf("delegated ExecutePlan should succeed: %v", err)
	}
	if len(ran) != 2 || ran[0] != "search" || ran[1] != "get_datetime" {
		t.Fatalf("expected both tasks delegated to trust Exec; ran=%v", ran)
	}
}

// TestExecutePlanTrustedRejectionAborts confirms a rejected gate yields zero
// side effects and surfaces as an error from ExecutePlan.
func TestExecutePlanTrustedRejectionAborts(t *testing.T) {
	var ran []string
	te := &trust.TrustedExecutor{
		Classifier: trust.NewRiskClassifier(),
		Verifier:   trust.NewStepVerifier(nil),
		Recoverer:  trust.NewLadderRecoverer(2),
		Confirm:    func(string) bool { return false },
		Exec: func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
			ran = append(ran, tool)
			return "ok", nil
		},
	}
	e := &GraphExecutor{Registry: nil, Trusted: te}
	plan := Plan{
		Transcript: "risky",
		Tasks:      []Task{{Tool: "delete_file", Params: json.RawMessage(`{}`)}},
	}
	if err := e.ExecutePlan(context.Background(), plan); err == nil {
		t.Fatal("rejected gate should return an error from ExecutePlan")
	}
	if len(ran) != 0 {
		t.Fatalf("rejected gate must run nothing; ran=%v", ran)
	}
}

// TestExecutePlanNilTrustedDefaults guards that Trusted defaults to nil so the
// legacy loop remains the default path (behavior unchanged from before).
func TestExecutePlanNilTrustedDefaults(t *testing.T) {
	e := NewExecutor(nil)
	if e.Trusted != nil {
		t.Fatal("Trusted must default to nil (legacy path)")
	}
}
