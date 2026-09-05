package dispatch

import "strings"

// InteractionMode is the up-front decision about how a request should be handled
// and presented — the "can I just do this / is this a long task" question from
// the interaction model. It lets the island pick its mode deliberately instead
// of inferring it after the fact.
type InteractionMode int

const (
	// ModeSimple: an instant action or answer — a single-turn status that
	// collapses. The vast majority of requests.
	ModeSimple InteractionMode = iota
	// ModeTask: a long / multi-step / bulk operation — gets a progress card
	// (and may pause for mid-task decisions) rather than a one-shot status.
	ModeTask
)

func (m InteractionMode) String() string {
	if m == ModeTask {
		return "task"
	}
	return "simple"
}

// taskCues mark a request as a long/bulk/multi-step Task. Kept deliberately
// conservative: a false Simple just means no progress card (cheap), while a
// false Task would show a card for something quick (mildly wrong), so we bias
// toward Simple and only escalate on clear signals.
var taskCues = []string{
	"organize", "organise", "clean up", "clean out", "tidy up",
	"sort all", "sort my", "rename all", "rename every",
	"for each", "every file", "all my files", "all the files", "each of my",
	"go through all", "go through my", "batch", "in bulk",
	"research", "deep dive", "look into", "investigate",
	"plan a", "plan my", "plan the", "summarize all", "summarise all",
	"delete all", "move all", "back up", "backup all",
}

// classifyInteraction returns the interaction mode for a request. Pure and
// testable; the dispatcher uses it to choose the initial presentation.
func classifyInteraction(input string) InteractionMode {
	l := strings.ToLower(input)
	for _, kw := range taskCues {
		if strings.Contains(l, kw) {
			return ModeTask
		}
	}
	return ModeSimple
}
