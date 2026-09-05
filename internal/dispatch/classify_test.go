package dispatch

import "testing"

func TestClassifyInteraction(t *testing.T) {
	task := []string{
		"organize my downloads folder",
		"research the best laptops under 1000",
		"plan a trip to bangalore next weekend",
		"rename all the screenshots",
		"summarize all my unread emails",
	}
	for _, in := range task {
		if classifyInteraction(in) != ModeTask {
			t.Errorf("%q should be ModeTask", in)
		}
	}
	simple := []string{
		"open notepad",
		"what time is it",
		"close chrome",
		"play music",
		"open my resume",
	}
	for _, in := range simple {
		if classifyInteraction(in) != ModeSimple {
			t.Errorf("%q should be ModeSimple", in)
		}
	}
}
