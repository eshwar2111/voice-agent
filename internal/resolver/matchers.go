package resolver

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/yourname/voice-agent/internal/agent"
)

// taskJSON builds an agent.Task, marshaling params to JSON (empty object on nil/err).
func taskJSON(tool string, params any) agent.Task {
	if params == nil {
		return agent.Task{Tool: tool, Params: json.RawMessage(`{}`)}
	}
	b, err := json.Marshal(params)
	if err != nil {
		b = []byte(`{}`)
	}
	return agent.Task{Tool: tool, Params: b}
}

// containsAny reports whether s contains any of subs.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

type DateTimeMatcher struct{}

func (DateTimeMatcher) Name() string { return "datetime" }
func (DateTimeMatcher) Match(in NormalizedInput) (*Match, bool) {
	if containsAny(in.Lower, "what time", "current time", "the time", "what's the date", "what is the date", "today's date") {
		return &Match{Tasks: []agent.Task{taskJSON("get_datetime", nil)}, Confidence: 0.95, Reason: "datetime phrase"}, true
	}
	return nil, false
}

// domainRe matches a bare domain or URL like "youtube.com" or "https://x.io/y".
var domainRe = regexp.MustCompile(`\b([a-z0-9-]+\.(com|org|net|io|dev|ai|co|gov|edu))(/\S*)?\b`)

type WebMatcher struct{}

func (WebMatcher) Name() string { return "web" }
func (WebMatcher) Match(in NormalizedInput) (*Match, bool) {
	// 1) explicit URL/domain anywhere in the input -> open_website
	if loc := domainRe.FindString(in.Lower); loc != "" {
		url := loc
		if !strings.HasPrefix(url, "http") {
			url = "https://" + url
		}
		return &Match{
			Tasks:      []agent.Task{taskJSON("open_website", map[string]string{"url": url})},
			Confidence: 0.9, Reason: "domain detected",
		}, true
	}
	// 2) "search X" / "google X" -> web_search
	for _, verb := range []string{"search ", "google ", "look up "} {
		if strings.HasPrefix(in.Lower, verb) {
			q := strings.TrimSpace(strings.TrimPrefix(in.Lower, verb))
			if q == "" {
				return nil, false
			}
			return &Match{
				Tasks:      []agent.Task{taskJSON("web_search", map[string]string{"query": q})},
				Confidence: 0.85, Reason: "search verb",
			}, true
		}
	}
	return nil, false
}

type AppMatcher struct {
	// Lookup returns the best-matching app display name and the number of apps matched.
	Lookup func(query string) (name string, count int)
}

var appLaunchVerbs = []string{"open ", "launch ", "start ", "run "}

func (a AppMatcher) Name() string { return "app" }
func (a AppMatcher) Match(in NormalizedInput) (*Match, bool) {
	var query string
	for _, v := range appLaunchVerbs {
		if strings.HasPrefix(in.Lower, v) {
			query = strings.TrimSpace(strings.TrimPrefix(in.Lower, v))
			break
		}
	}
	if query == "" || a.Lookup == nil {
		return nil, false
	}
	name, count := a.Lookup(query)
	if count == 0 || name == "" {
		return nil, false
	}
	conf := 0.9
	if count > 1 {
		conf = 0.5 // ambiguous -> below threshold, falls to Tier 1 / disambiguation
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("open_app", map[string]string{"app_name": name})},
		Confidence: conf, Reason: "app launch verb",
	}, true
}

type FileMatcher struct {
	Search func(query string) []string // returns candidate absolute paths
}

func (f FileMatcher) Name() string { return "file" }
func (f FileMatcher) Match(in NormalizedInput) (*Match, bool) {
	// require an explicit "file" cue to avoid stealing app launches
	if !strings.HasPrefix(in.Lower, "open file ") && !strings.HasPrefix(in.Lower, "find file ") {
		return nil, false
	}
	query := strings.TrimSpace(in.Lower)
	query = strings.TrimPrefix(query, "open file ")
	query = strings.TrimPrefix(query, "find file ")
	if query == "" || f.Search == nil {
		return nil, false
	}
	hits := f.Search(query)
	if len(hits) == 0 {
		return nil, false
	}
	conf := 0.85
	if len(hits) > 1 {
		conf = 0.5
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("open_file", map[string]string{"file_path": hits[0]})},
		Confidence: conf, Reason: "file cue + index hit",
	}, true
}
