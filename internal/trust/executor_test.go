package trust

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// helper: an executor with real units but fake side effects, backoff disabled.
func newTestExec() (*TrustedExecutor, *[]string) {
	var execed []string
	te := &TrustedExecutor{
		Classifier: NewRiskClassifier(),
		Verifier:   NewStepVerifier(nil),
		Recoverer:  NewLadderRecoverer(2),
		Describe:   func(tool string, p json.RawMessage) string { return tool },
		Narrate:    func(string) {},
		Exec: func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
			execed = append(execed, tool)
			return "ok", nil
		},
	}
	te.noBackoff() // test seam: disable sleeps
	return te, &execed
}

// TestRunReplanGuardResetsPerPlan guards the per-plan (not per-process) re-plan
// budget: the same executor/recoverer is reused across two plans, and each plan
// must independently reach its Ask rung. Before the Run-start Reset() fix, the
// second plan's guard stayed latched and it never asked.
func TestRunReplanGuardResetsPerPlan(t *testing.T) {
	replanCalls := 0
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		return "", errors.New("always fails")
	}
	// Count re-plan attempts: this is the observable that the per-process latch
	// suppresses. With the latch unreset, plan 2 skips Replan entirely (Recover
	// returns Ask directly) → replanCalls would be 1, not 2.
	te.Replan = func(ctx context.Context, r []Step, f Step, e error) []Step { replanCalls++; return nil }
	te.Ask = func(step Step, reason string) Decision { return Abort }

	rep1, _ := te.Run(context.Background(), []Step{{Tool: "flaky"}}, "cmd1")
	rep2, _ := te.Run(context.Background(), []Step{{Tool: "flaky"}}, "cmd2")

	if !rep1.Aborted || !rep2.Aborted {
		t.Fatalf("both plans should abort; rep1=%+v rep2=%+v", rep1, rep2)
	}
	if replanCalls != 2 {
		t.Fatalf("each plan must independently get its one re-plan; got replanCalls=%d want 2", replanCalls)
	}
}

// TestRunEmptyReplanFallsThroughToAsk: when Replan yields no steps (v1 no-op),
// the executor asks the user rather than aborting outright.
func TestRunEmptyReplanFallsThroughToAsk(t *testing.T) {
	askedRetry := false
	calls := 0
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		calls++
		if calls <= 3 { // first run + 2 retries fail, then user-retry succeeds
			return "", errors.New("fail")
		}
		return "ok", nil
	}
	te.Replan = func(ctx context.Context, r []Step, f Step, e error) []Step { return nil }
	te.Ask = func(step Step, reason string) Decision { askedRetry = true; return Retry }

	rep, err := te.Run(context.Background(), []Step{{Tool: "flaky"}}, "cmd")
	if err != nil || rep.Aborted {
		t.Fatalf("user-retry should let it recover; rep=%+v err=%v", rep, err)
	}
	if !askedRetry {
		t.Fatal("empty replan must fall through to Ask, not stop")
	}
}

func TestRunSingleSafeNoGate(t *testing.T) {
	te, execed := newTestExec()
	gated := false
	te.Confirm = func(string) bool { gated = true; return true }
	rep, err := te.Run(context.Background(), []Step{{Tool: "search"}}, "find x")
	if err != nil || gated {
		t.Fatalf("single safe step must not gate; gated=%v err=%v", gated, err)
	}
	if len(*execed) != 1 || rep.Aborted {
		t.Fatalf("should have run 1 step; rep=%+v execed=%v", rep, *execed)
	}
}

func TestRunGateRejectZeroSideEffects(t *testing.T) {
	te, execed := newTestExec()
	te.Confirm = func(string) bool { return false }
	rep, _ := te.Run(context.Background(), []Step{{Tool: "search"}, {Tool: "delete_file"}}, "cmd")
	if len(*execed) != 0 || !rep.Aborted {
		t.Fatalf("reject must run nothing; execed=%v rep=%+v", *execed, rep)
	}
}

func TestRunGateApproveRunsAll(t *testing.T) {
	te, execed := newTestExec()
	te.Confirm = func(string) bool { return true }
	_, err := te.Run(context.Background(), []Step{{Tool: "search"}, {Tool: "search"}}, "cmd")
	if err != nil || len(*execed) != 2 {
		t.Fatalf("approve should run both; execed=%v err=%v", *execed, err)
	}
}

func TestRunRetryThenSucceed(t *testing.T) {
	var calls int
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("transient")
		}
		return "ok", nil
	}
	_, err := te.Run(context.Background(), []Step{{Tool: "search"}}, "cmd")
	if err != nil || calls != 2 {
		t.Fatalf("expected 1 retry then success; calls=%d err=%v", calls, err)
	}
}

func TestRunReplanOnceThenTail(t *testing.T) {
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	// step always fails; replan returns a single safe step that succeeds.
	fail := true
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		if tool == "flaky" {
			return "", errors.New("nope")
		}
		return "ok", nil
	}
	replans := 0
	te.Replan = func(ctx context.Context, remaining []Step, failed Step, err error) []Step {
		replans++
		return []Step{{Tool: "search"}}
	}
	_ = fail
	_, err := te.Run(context.Background(), []Step{{Tool: "flaky"}}, "cmd")
	if replans != 1 || err != nil {
		t.Fatalf("expected exactly one replan then success; replans=%d err=%v", replans, err)
	}
}

func TestRunAbortReport(t *testing.T) {
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		return "", errors.New("always")
	}
	te.Replan = func(ctx context.Context, r []Step, f Step, e error) []Step { return nil } // no replan
	te.Ask = func(step Step, reason string) Decision { return Abort }
	rep, _ := te.Run(context.Background(), []Step{{Tool: "flaky"}}, "cmd")
	if !rep.Aborted || rep.FailedAt != 0 {
		t.Fatalf("expected abort report at step 0; rep=%+v", rep)
	}
}

func TestRunPreviousOutputInjection(t *testing.T) {
	te, _ := newTestExec()
	te.Confirm = func(string) bool { return true }
	var seen string
	te.Exec = func(ctx context.Context, tool string, p json.RawMessage) (string, error) {
		if tool == "second" {
			var m map[string]string
			json.Unmarshal(p, &m)
			seen = m["text"]
		}
		return "RESULT1", nil
	}
	p2, _ := json.Marshal(map[string]string{"text": "{PREVIOUS_OUTPUT}"})
	_, err := te.Run(context.Background(), []Step{{Tool: "first"}, {Tool: "second", Params: p2}}, "cmd")
	if err != nil || seen != "RESULT1" {
		t.Fatalf("PREVIOUS_OUTPUT not injected; seen=%q err=%v", seen, err)
	}
}
