// Package task implements the durable multi-step TaskSession state machine: a
// task with an ordered list of steps (actions, and decisions the user answers),
// accumulating context in Data, persisted after every step so it survives across
// the many user/agent exchanges — and, because steps are DATA (a tool name +
// params, not Go closures), across an app restart. The session state is internal
// to this machine; the island only ever shows a progress card / a single
// question, never a chat log.
package task

import (
	"encoding/json"
	"strings"
	"time"
)

// State is the session lifecycle state.
type State string

const (
	StateRunning   State = "running"
	StateWaiting   State = "waiting" // blocked on a user decision
	StateDone      State = "done"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// StepKind is what a step does.
type StepKind string

const (
	StepAction    StepKind = "action"     // run a tool
	StepAskText   StepKind = "ask_text"   // ask the user free text
	StepAskChoice StepKind = "ask_choice" // ask the user to pick an option
	StepConfirm   StepKind = "confirm"    // ask the user to approve
)

// Choice is one option for an ask_choice step.
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Sub   string `json:"sub,omitempty"`
}

// Step is one node of the task. Everything is data so the whole session
// serializes and resumes. For StepAction, Tool+Params run a registered tool;
// Params values may reference accumulated data with {{key}} placeholders. For ask
// steps, the user's answer is stored under StoreAs. Label is a short human phrase
// for the progress card.
type Step struct {
	Kind    StepKind          `json:"kind"`
	Label   string            `json:"label,omitempty"`
	Prompt  string            `json:"prompt,omitempty"`  // ask_text / ask_choice / confirm
	Options []Choice          `json:"options,omitempty"` // ask_choice
	StoreAs string            `json:"store_as,omitempty"`
	Tool    string            `json:"tool,omitempty"`   // action
	Params  map[string]string `json:"params,omitempty"` // action (values may use {{key}})
}

// Session is a durable multi-step task.
type Session struct {
	ID        string            `json:"id"`
	Goal      string            `json:"goal"`
	Steps     []Step            `json:"steps"`
	Cursor    int               `json:"cursor"`
	State     State             `json:"state"`
	Data      map[string]string `json:"data"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// NewSession builds a running session with a stable id.
func NewSession(id, goal string, steps []Step) *Session {
	now := time.Now()
	return &Session{
		ID: id, Goal: goal, Steps: steps, Cursor: 0, State: StateRunning,
		Data: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
}

// Done reports whether the session has reached a terminal state.
func (s *Session) Done() bool {
	return s.State == StateDone || s.State == StateFailed || s.State == StateCancelled
}

func (s *Session) touch() { s.UpdatedAt = time.Now() }

// interpolate replaces {{key}} in a string with s.Data[key] (missing keys ->
// empty). Used to weave earlier answers into later action params/prompts.
func (s *Session) interpolate(in string) string {
	if !strings.Contains(in, "{{") {
		return in
	}
	out := in
	for k, v := range s.Data {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// paramsJSON builds the interpolated JSON params for an action step.
func (s *Session) paramsJSON(step Step) json.RawMessage {
	m := make(map[string]string, len(step.Params))
	for k, v := range step.Params {
		m[k] = s.interpolate(v)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
