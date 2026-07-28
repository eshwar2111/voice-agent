package trust

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const prevOutputToken = "{PREVIOUS_OUTPUT}"

// backoff is overridable in tests via noBackoff().
func (t *TrustedExecutor) sleep(d time.Duration) {
	if t.backoff == nil {
		time.Sleep(d)
		return
	}
	t.backoff(d)
}

// add a private field via a struct method file — declared here to keep executor cohesive.
// (field is added to the struct in types.go; see note below.)

func (t *TrustedExecutor) noBackoff() { t.backoff = func(time.Duration) {} }

func (t *TrustedExecutor) classifyAll(steps []Step) []Risk {
	risks := make([]Risk, len(steps))
	for i, s := range steps {
		risks[i] = t.Classifier.Classify(s.Tool, s.Params)
	}
	return risks
}

func injectPrev(params json.RawMessage, prev string) json.RawMessage {
	if len(params) == 0 || !strings.Contains(string(params), prevOutputToken) {
		return params
	}
	var m map[string]any
	if json.Unmarshal(params, &m) != nil {
		return params
	}
	for k, v := range m {
		if sv, ok := v.(string); ok {
			m[k] = strings.ReplaceAll(sv, prevOutputToken, prev)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return params
	}
	return b
}

func (t *TrustedExecutor) Run(ctx context.Context, steps []Step, command string) (Report, error) {
	rep := Report{FailedAt: -1}
	// Reset the once-per-plan re-plan guard — the executor holds one shared
	// recoverer across every plan, so without this the budget would latch
	// process-wide after the first plan that reaches the re-plan rung.
	if r, ok := t.Recoverer.(*LadderRecoverer); ok {
		r.Reset()
	}
	risks := t.classifyAll(steps)

	// One gate up front.
	if ShouldGate(steps, risks) && t.Confirm != nil {
		if !t.Confirm(BuildPreview(command, steps, risks, t.Describe)) {
			rep.Aborted = true
			rep.FailNote = "cancelled by user"
			return rep, nil
		}
	}

	var lastOutput string
	i := 0
	for i < len(steps) {
		step := steps[i]
		attempt := 0
		for { // retry loop for this step
			attempt++
			label := t.describeStep(step)
			if t.Narrate != nil {
				t.Narrate(fmt.Sprintf("Step %d/%d · %s…", i+1, len(steps), label))
			}
			params := injectPrev(step.Params, lastOutput)
			result, execErr := t.Exec(ctx, step.Tool, params)
			ok, reason := t.Verifier.Verify(ctx, step, result, execErr)
			if ok {
				lastOutput = result
				rep.Completed = append(rep.Completed, label)
				break // step done
			}
			// Recover.
			stepErr := execErr
			if stepErr == nil {
				stepErr = fmt.Errorf("%s", reason)
			}
			switch t.Recoverer.Recover(step, attempt, stepErr) {
			case Retry:
				t.sleep(150 * time.Millisecond)
				continue
			case Replan:
				tail := []Step(nil)
				if t.Replan != nil {
					tail = t.Replan(ctx, steps[i+1:], step, stepErr)
				}
				if r, ok := t.Recoverer.(*LadderRecoverer); ok {
					r.MarkReplanned()
				}
				if len(tail) == 0 {
					// No re-plan available (e.g. v1 no-op Replan). Fall through to
					// asking the user rather than aborting outright.
					if t.Ask != nil && t.Ask(step, reason) == Retry {
						t.sleep(150 * time.Millisecond)
						continue
					}
					return t.stop(rep, i, "stopped at failing step: "+stepErr.Error())
				}
				// Replace remaining tail; re-classify (no second gate).
				steps = append(steps[:i], tail...)
				risks = t.classifyAll(steps)
				attempt = 0
				step = steps[i]
				continue
			case Ask:
				if t.Ask != nil && t.Ask(step, reason) == Retry {
					t.sleep(150 * time.Millisecond)
					continue
				}
				return t.stop(rep, i, "stopped at failing step: "+stepErr.Error())
			default: // Abort
				return t.stop(rep, i, stepErr.Error())
			}
		}
		i++
	}
	return rep, nil
}

func (t *TrustedExecutor) stop(rep Report, at int, note string) (Report, error) {
	rep.Aborted = true
	rep.FailedAt = at
	rep.FailNote = note
	if t.Narrate != nil {
		t.Narrate("Stopped: " + note)
	}
	return rep, nil
}

func (t *TrustedExecutor) describeStep(step Step) string {
	if t.Describe != nil {
		if d := t.Describe(step.Tool, step.Params); d != "" {
			return d
		}
	}
	if step.Goal != "" {
		return step.Goal
	}
	return step.Tool
}
