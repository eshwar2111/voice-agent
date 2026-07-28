// Package trust is the trustworthy one-shot execution layer. It wraps a plan's
// steps with risk classification, a single up-front approval gate, cheap-first
// verification, and a bounded recovery ladder. It imports only the standard
// library; all coupling to the rest of the app flows inward via injected funcs.
package trust

import (
	"context"
	"encoding/json"
	"time"
)

// Step is one unit of a plan, carrying the plain-English intent for previews
// and LLM judging. trust owns this type; agent converts its Task into it.
type Step struct {
	Tool   string
	Params json.RawMessage
	Goal   string // plain-English intent, e.g. "delete the old invoice"
}

type Risk int

const (
	Safe Risk = iota
	Risky
)

func (r Risk) String() string {
	if r == Risky {
		return "Risky"
	}
	return "Safe"
}

type Decision int

const (
	Retry Decision = iota
	Replan
	Ask
	Abort
)

// Report is returned by Run so the caller can render what completed vs. failed.
type Report struct {
	Completed []string // Describe() text of steps that ran and verified
	FailedAt  int      // index of the failing step, or -1
	FailNote  string   // human-readable reason for the stop
	Aborted   bool
}

type Classifier interface {
	Classify(tool string, params json.RawMessage) Risk
}

type Verifier interface {
	// ok=false means the step did not achieve its goal; reason is for narration.
	Verify(ctx context.Context, step Step, result string, execErr error) (ok bool, reason string)
}

type Recoverer interface {
	Recover(step Step, attempt int, lastErr error) Decision
}

// TrustedExecutor composes the units and injected side effects. trust never
// imports agent/tools/ui/llm; those are supplied as funcs.
type TrustedExecutor struct {
	Classifier Classifier
	Verifier   Verifier
	Recoverer  Recoverer

	Exec     func(ctx context.Context, tool string, params json.RawMessage) (string, error)
	Confirm  func(previewJSON string) bool
	Describe func(tool string, params json.RawMessage) string
	Narrate  func(msg string)
	Replan   func(ctx context.Context, remaining []Step, failed Step, err error) []Step
	Ask      func(step Step, reason string) Decision

	backoff func(time.Duration) // nil = real time.Sleep; tests set a no-op
}
