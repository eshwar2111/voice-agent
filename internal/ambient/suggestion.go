package ambient

import "context"

// Suggestion is one proactive, actionable prompt shown as a card.
type Suggestion struct {
	Source   string // "downloads" | "calendar" | "clipboard"
	Icon     string // badge glyph key: "download"|"calendar"|"link"|"warn"
	Title    string
	Message  string
	Action   string // primary button label, e.g. "Unzip"
	DedupKey string // suppress repeats
	Run      func(ctx context.Context) error
}

// Source watches for events and emits Suggestions until ctx is cancelled.
type Source interface {
	Name() string
	Start(ctx context.Context, out chan<- Suggestion)
}

// Deliverer shows a suggestion to the user (implemented by the UI).
type Deliverer interface {
	ShowSuggestion(id string, s Suggestion)
}

// DelivererFunc adapts a function to the Deliverer interface.
type DelivererFunc func(id, icon, title, message, action string)

func (f DelivererFunc) ShowSuggestion(id string, s Suggestion) {
	f(id, s.Icon, s.Title, s.Message, s.Action)
}
