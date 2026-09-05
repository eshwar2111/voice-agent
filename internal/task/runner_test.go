package task

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeDeps builds Deps backed by scripted answers, recording executed actions.
type recorder struct {
	execCalls []string          // "tool:paramsJSON"
	textAns   map[string]string // prompt -> answer ("" absent => cancel)
	choiceAns map[string]string // prompt -> chosen id
	confirm   bool
}

func (r *recorder) deps(store func(*Session)) Deps {
	return Deps{
		Exec: func(_ context.Context, tool string, params json.RawMessage) (string, error) {
			r.execCalls = append(r.execCalls, tool+":"+string(params))
			return "ok(" + tool + ")", nil
		},
		AskText: func(prompt string) (string, bool) {
			v, ok := r.textAns[prompt]
			return v, ok
		},
		AskChoice: func(prompt string, _ []Choice) (string, bool) {
			v, ok := r.choiceAns[prompt]
			return v, ok
		},
		Confirm: func(string) bool { return r.confirm },
		Save:    store,
	}
}

func emailSteps() []Step {
	return []Step{
		{Kind: StepAskText, Label: "Recipient", Prompt: "Who should I email?", StoreAs: "to"},
		{Kind: StepAskText, Label: "Subject", Prompt: "Subject?", StoreAs: "subject"},
		{Kind: StepConfirm, Label: "Confirm", Prompt: "Send to {{to}} — {{subject}}?"},
		{Kind: StepAction, Label: "Send", Tool: "gmail_send", StoreAs: "result",
			Params: map[string]string{"to": "{{to}}", "subject": "{{subject}}"}},
	}
}

func TestRunHappyPath(t *testing.T) {
	rec := &recorder{
		textAns: map[string]string{"Who should I email?": "rahul@x.com", "Subject?": "Hi"},
		confirm: true,
	}
	s := NewSession("t1", "Compose an email", emailSteps())
	if err := NewRunner(rec.deps(nil)).Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.State != StateDone {
		t.Fatalf("state = %s, want done", s.State)
	}
	// Accumulated context + interpolation reached the action.
	if len(rec.execCalls) != 1 || rec.execCalls[0] != `gmail_send:{"subject":"Hi","to":"rahul@x.com"}` {
		t.Fatalf("action params wrong: %v", rec.execCalls)
	}
	if s.Data["result"] != "ok(gmail_send)" {
		t.Fatalf("action output not stored: %q", s.Data["result"])
	}
}

func TestRunCancelAtConfirm(t *testing.T) {
	rec := &recorder{
		textAns: map[string]string{"Who should I email?": "a@b.com", "Subject?": "Hi"},
		confirm: false, // decline
	}
	s := NewSession("t2", "email", emailSteps())
	_ = NewRunner(rec.deps(nil)).Run(context.Background(), s)
	if s.State != StateCancelled {
		t.Fatalf("state = %s, want cancelled", s.State)
	}
	if len(rec.execCalls) != 0 {
		t.Fatalf("no action should run after a declined confirm, got %v", rec.execCalls)
	}
}

func TestRunCancelAtAsk(t *testing.T) {
	rec := &recorder{textAns: map[string]string{}} // no answer => AskText returns ok=false
	s := NewSession("t3", "email", emailSteps())
	_ = NewRunner(rec.deps(nil)).Run(context.Background(), s)
	if s.State != StateCancelled {
		t.Fatalf("state = %s, want cancelled", s.State)
	}
	if s.Cursor != 0 {
		t.Fatalf("cursor should stay at the cancelled step, got %d", s.Cursor)
	}
}

// TestResumeFromPersistedState proves durability: a session interrupted after
// its first answers reconstructs from saved state and finishes without re-asking
// the completed steps.
func TestResumeFromPersistedState(t *testing.T) {
	// First pass: answer the recipient, then the process "crashes" while blocked
	// on the subject ask. A crash leaves the LAST persisted running snapshot
	// (Save is called after each completed step), which is what we capture here —
	// NOT a user cancel (which is deliberately terminal / non-resumable).
	var crashSnapshot []byte
	saveRunning := func(sess *Session) {
		if sess.State == StateRunning {
			crashSnapshot, _ = json.Marshal(sess)
		}
	}
	rec1 := &recorder{textAns: map[string]string{"Who should I email?": "rahul@x.com"}}
	s := NewSession("t4", "email", emailSteps())
	_ = NewRunner(rec1.deps(saveRunning)).Run(context.Background(), s)
	if crashSnapshot == nil {
		t.Fatal("expected a running snapshot to be persisted after step 0")
	}

	// Reload the crash snapshot — simulating a restart from disk.
	var loaded Session
	if err := json.Unmarshal(crashSnapshot, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Cursor != 1 || loaded.Data["to"] != "rahul@x.com" || loaded.Done() {
		t.Fatalf("crash snapshot wrong: cursor=%d to=%q state=%s", loaded.Cursor, loaded.Data["to"], loaded.State)
	}

	// Resume: now answer the rest. It must NOT re-ask the recipient.
	rec2 := &recorder{
		textAns: map[string]string{"Subject?": "Hi"},
		confirm: true,
	}
	if err := NewRunner(rec2.deps(nil)).Run(context.Background(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.State != StateDone {
		t.Fatalf("resumed state = %s, want done", loaded.State)
	}
	if loaded.Data["to"] != "rahul@x.com" || loaded.Data["subject"] != "Hi" {
		t.Fatalf("resumed data lost: %+v", loaded.Data)
	}
	if len(rec2.execCalls) != 1 {
		t.Fatalf("resumed run should execute the final action once, got %v", rec2.execCalls)
	}
}
