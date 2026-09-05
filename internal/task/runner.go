package task

import (
	"context"
	"encoding/json"
	"fmt"
)

// ProgressReporter drives a progress card (satisfied by *ui.ProgressHandle).
type ProgressReporter interface {
	Update(done, total int, note string)
	Done(summary string)
	Fail(msg string)
}

// Deps are the injected capabilities the runner needs — kept as funcs so the
// task package imports neither ui nor tools (no cycles, and fully testable with
// fakes). Any may be nil except Exec (actions need it).
type Deps struct {
	// Exec runs a registered tool and returns its summary output.
	Exec func(ctx context.Context, tool string, params json.RawMessage) (string, error)
	// AskText / AskChoice / Confirm gather user decisions (block until answered;
	// the bool is false on cancel).
	AskText   func(prompt string) (string, bool)
	AskChoice func(prompt string, options []Choice) (id string, ok bool)
	Confirm   func(prompt string) bool
	// StartProgress opens a progress card; cancel is invoked if the user Stops.
	// nil disables the card. The returned reporter may be nil.
	StartProgress func(title string, cancel func()) ProgressReporter
	// Save persists the session after each step (durability). nil disables it.
	Save func(*Session)
}

// Runner executes sessions to completion.
type Runner struct {
	Deps Deps
}

func NewRunner(d Deps) *Runner { return &Runner{Deps: d} }

func (r *Runner) save(s *Session) {
	if r.Deps.Save != nil {
		r.Deps.Save(s)
	}
}

// Run executes the session from its current Cursor to a terminal state,
// persisting after every step so it can be resumed (Run again with the loaded
// session) if interrupted. Blocking asks are answered via the injected Ask/
// Confirm funcs. Safe to call on an already-terminal session (no-op).
func (r *Runner) Run(ctx context.Context, s *Session) error {
	if s.Done() {
		return nil
	}
	s.State = StateRunning
	var prog ProgressReporter
	if r.Deps.StartProgress != nil {
		prog = r.Deps.StartProgress(s.Goal, func() { s.State = StateCancelled })
	}

	for s.Cursor < len(s.Steps) {
		if err := ctx.Err(); err != nil {
			s.State = StateCancelled
			break
		}
		if s.State == StateCancelled {
			break
		}
		step := s.Steps[s.Cursor]
		if prog != nil {
			prog.Update(s.Cursor, len(s.Steps), r.stepNote(s, step))
		}

		switch step.Kind {
		case StepAction:
			if r.Deps.Exec == nil {
				return r.fail(s, prog, fmt.Errorf("no executor configured for action %q", step.Tool))
			}
			out, err := r.Deps.Exec(ctx, step.Tool, s.paramsJSON(step))
			if err != nil {
				return r.fail(s, prog, fmt.Errorf("step %q (%s): %w", step.Label, step.Tool, err))
			}
			if step.StoreAs != "" {
				s.Data[step.StoreAs] = out
			}

		case StepAskText:
			if r.Deps.AskText == nil {
				return r.fail(s, prog, fmt.Errorf("no AskText for step %q", step.Label))
			}
			ans, ok := r.Deps.AskText(s.interpolate(step.Prompt))
			if !ok {
				s.State = StateCancelled
			} else if step.StoreAs != "" {
				s.Data[step.StoreAs] = ans
			}

		case StepAskChoice:
			if r.Deps.AskChoice == nil {
				return r.fail(s, prog, fmt.Errorf("no AskChoice for step %q", step.Label))
			}
			id, ok := r.Deps.AskChoice(s.interpolate(step.Prompt), step.Options)
			if !ok {
				s.State = StateCancelled
			} else if step.StoreAs != "" {
				s.Data[step.StoreAs] = id
			}

		case StepConfirm:
			if r.Deps.Confirm == nil || !r.Deps.Confirm(s.interpolate(step.Prompt)) {
				s.State = StateCancelled
			}

		default:
			return r.fail(s, prog, fmt.Errorf("unknown step kind %q", step.Kind))
		}

		if s.State == StateCancelled {
			break
		}
		s.Cursor++
		s.touch()
		r.save(s)
	}

	if s.State == StateCancelled {
		s.touch()
		r.save(s)
		if prog != nil {
			prog.Fail("Cancelled")
		}
		return nil
	}
	s.State = StateDone
	s.touch()
	r.save(s)
	if prog != nil {
		prog.Done("Done")
	}
	return nil
}

func (r *Runner) fail(s *Session, prog ProgressReporter, err error) error {
	s.State = StateFailed
	s.touch()
	r.save(s)
	if prog != nil {
		prog.Fail(err.Error())
	}
	return err
}

// stepNote is the human line shown on the progress card for a step.
func (r *Runner) stepNote(s *Session, step Step) string {
	if step.Label != "" {
		return step.Label
	}
	switch step.Kind {
	case StepAction:
		return "Running " + step.Tool
	case StepAskText, StepAskChoice, StepConfirm:
		return s.interpolate(step.Prompt)
	}
	return "Working…"
}
