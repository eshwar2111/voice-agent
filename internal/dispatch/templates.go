package dispatch

import (
	"strings"
	"time"

	"github.com/yourname/voice-agent/internal/task"
)

// Task templates: a bare task-launching phrase (no details) becomes a durable,
// guided multi-step TaskSession. A fully-specified command ("email bob the
// notes") stays one-shot through the normal path — only a request with no
// specifics turns conversational, matching the "don't be annoying" principle.

var emailLaunchers = []string{
	"compose an email", "write an email", "send an email", "draft an email",
	"compose email", "write email", "send email", "draft email",
	"new email", "send a mail", "write a mail", "send an mail",
}

// taskFor returns a TaskSession for a recognized bare launcher, else nil.
func taskFor(input string) *task.Session {
	l := strings.TrimRight(strings.ToLower(strings.TrimSpace(input)), ".!?")
	for _, p := range emailLaunchers {
		if l == p {
			return emailTask()
		}
	}
	return nil
}

// emailTask is the conversational "compose an email" flow: gather recipient,
// subject and body across turns, confirm, then send (the send tool itself does
// contact disambiguation on the recipient). Steps are data, so this session
// persists and resumes.
func emailTask() *task.Session {
	id := "email-" + time.Now().Format("20060102-150405.000")
	return task.NewSession(id, "Compose an email", []task.Step{
		{Kind: task.StepAskText, Label: "Recipient", Prompt: "Who should I send it to?", StoreAs: "to"},
		{Kind: task.StepAskText, Label: "Subject", Prompt: "What's the subject?", StoreAs: "subject"},
		{Kind: task.StepAskText, Label: "Message", Prompt: "What should the email say?", StoreAs: "body"},
		{Kind: task.StepConfirm, Label: "Confirm", Prompt: `Send to "{{to}}" — subject "{{subject}}"?`},
		{Kind: task.StepAction, Label: "Send", Tool: "google_gmail_send", StoreAs: "result",
			Params: map[string]string{"to": "{{to}}", "subject": "{{subject}}", "body": "{{body}}"}},
	})
}
