package trust

import (
	"errors"
	"testing"
)

func TestRecoverLadder(t *testing.T) {
	r := NewLadderRecoverer(2)
	err := errors.New("boom")
	if d := r.Recover(Step{}, 1, err); d != Retry {
		t.Errorf("attempt 1 → Retry, got %v", d)
	}
	if d := r.Recover(Step{}, 2, err); d != Retry {
		t.Errorf("attempt 2 → Retry, got %v", d)
	}
	if d := r.Recover(Step{}, 3, err); d != Replan {
		t.Errorf("attempt 3 (retries exhausted) → Replan, got %v", d)
	}
	r.MarkReplanned()
	if d := r.Recover(Step{}, 3, err); d != Ask {
		t.Errorf("after replan used → Ask, got %v", d)
	}
}

func TestRecoverZeroRetries(t *testing.T) {
	r := NewLadderRecoverer(0)
	if d := r.Recover(Step{}, 1, errors.New("x")); d != Replan {
		t.Errorf("0 retries, attempt 1 → Replan, got %v", d)
	}
}
