package resolver

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/yourname/voice-agent/internal/agent"
	"github.com/yourname/voice-agent/internal/executor"
	"github.com/yourname/voice-agent/internal/search"
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

// isCommandPhrase reports whether s IS the given command, rather than merely
// containing it somewhere. Use this for short, common English words that double
// as ordinary vocabulary ("pause", "play", "stop") — an unanchored Contains on
// those lets Tier 0 claim a sentence whose real intent lives elsewhere, and
// because a claim short-circuits the chain, Tier 1 never gets to correct it.
func isCommandPhrase(s, cmd string) bool {
	s = strings.TrimSpace(s)
	if s == cmd {
		return true
	}
	// Allow a trailing object ("pause it", "pause spotify") but nothing before
	// the verb, so a mid-sentence use can never match.
	rest, ok := strings.CutPrefix(s, cmd+" ")
	return ok && len(strings.Fields(rest)) <= 2
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
	// file commands take precedence over domain-looking substrings (e.g. "resume.ai")
	if strings.HasPrefix(in.Lower, "open file ") || strings.HasPrefix(in.Lower, "find file ") {
		return nil, false
	}
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
			// "search FOR the weather" is at least as common in speech as bare
			// "search the weather", and stripping only the verb left the query as
			// "for the weather" — which Tier 0 then claimed at 0.85 confidence, so
			// Tier 1 never got the chance to clean it up. Every such search was
			// silently degraded by a stray preposition.
			for _, prep := range []string{"for ", "up "} {
				q = strings.TrimSpace(strings.TrimPrefix(q, prep))
			}
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

// naturalOpenPrefixes are the ways people actually ask to open a file out loud —
// "open my latest resume", "pull up the budget", "show me my notes". They carry
// no explicit "file" cue, so they only become a file-open AFTER the app matcher
// has passed (a real app always wins) and ONLY when the file index resolves a
// CONFIDENT hit. Ordered longest-first so "open my " strips before "open ".
var naturalOpenPrefixes = []string{
	"open up my ", "open up the ", "open up ",
	"open my ", "open the ", "open a ", "open ",
	"pull up my ", "pull up the ", "pull up ",
	"bring up my ", "bring up the ", "bring up ",
	"show me my ", "show me the ", "show me ", "show my ", "show the ", "show ",
	"get me my ", "get me the ", "get my ", "get me ", "get the ",
	"find my ", "find the ",
}

// NaturalFileMatcher is the last-resort local resolver for natural "open my X"
// phrasing. It never needs the cloud, and never opens the wrong thing: it emits
// an open_file plan ONLY when the file index confidently resolves the spoken
// name to a real path (Resolve applies the index's own rank threshold), so a
// query with no good file match falls through to Tier 1 rather than opening
// something random. Placed after AppMatcher so app launches keep priority.
type NaturalFileMatcher struct {
	Resolve func(query string) (path string, ok bool)
}

func (NaturalFileMatcher) Name() string { return "natural-file" }
func (n NaturalFileMatcher) Match(in NormalizedInput) (*Match, bool) {
	if n.Resolve == nil {
		return nil, false
	}
	var query string
	matched := false
	for _, p := range naturalOpenPrefixes {
		if strings.HasPrefix(in.Lower, p) {
			query = strings.TrimSpace(strings.TrimPrefix(in.Lower, p))
			matched = true
			break
		}
	}
	if !matched || query == "" {
		return nil, false
	}
	path, ok := n.Resolve(query)
	if !ok || path == "" {
		return nil, false
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("open_file", map[string]string{"file_path": path})},
		Confidence: 0.8, Reason: "natural open + confident index hit",
	}, true
}

type MediaMatcher struct{}

func (MediaMatcher) Name() string { return "media" }
func (MediaMatcher) Match(in NormalizedInput) (*Match, bool) {
	l := in.Lower
	var action string
	switch {
	case containsAny(l, "volume up", "louder", "turn it up"):
		action = "volume_up"
	case containsAny(l, "volume down", "quieter", "turn it down"):
		action = "volume_down"
	case containsAny(l, "mute", "unmute"):
		action = "mute"
	case containsAny(l, "next track", "next song", "skip track", "skip song"):
		action = "next"
	case containsAny(l, "previous track", "previous song", "go back a track"):
		action = "previous"
	// Anchored, not contains. "pause" as an unanchored substring claimed any
	// sentence that merely used the word — "pause for a second, what's on my
	// calendar?" was resolved to media_control{pause} at 0.9 confidence and the
	// real request was thrown away before Tier 1 ever saw it. Mirrors how the
	// "play" case below was already anchored.
	case isCommandPhrase(l, "pause"):
		action = "pause"
	case containsAny(l, "pause music", "pause the music", "pause playback", "pause the song",
		"pause the movie", "pause the video", "pause the film", "pause the show",
		"pause it", "pause the media", "pause what"):
		// Generic play/pause via the Windows media key (media_control) works on
		// whatever is playing — a movie, a browser video, Spotify — with NO vision
		// model and NO cloud call. "play the movie on my screen" used to fall
		// through to Tier 1 → find_and_click (vision), which burned the LLM quota
		// for something a single keystroke does.
		action = "pause"
	case l == "play" || l == "resume" || l == "continue" || l == "unpause" ||
		containsAny(l, "play music", "resume music", "resume playback", "resume playing",
			"continue playing", "continue watching", "resume the", "continue the",
			"play the movie", "play the video", "play the film", "play the show",
			"play it", "resume it", "unpause", "play the paused", "play what"):
		action = "play"
	default:
		return nil, false
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("media_control", map[string]string{"action": action})},
		Confidence: 0.9, Reason: "media phrase",
	}, true
}

type SystemMatcher struct{}

func (SystemMatcher) Name() string { return "system" }
func (SystemMatcher) Match(in NormalizedInput) (*Match, bool) {
	l := in.Lower
	var action string
	switch {
	case containsAny(l, "lock the pc", "lock computer", "lock screen", "lock my"):
		action = "lock"
	case containsAny(l, "go to sleep", "sleep the pc", "put to sleep", "suspend"):
		action = "sleep"
	case containsAny(l, "brightness up", "brighter", "increase brightness"):
		action = "brightness_up"
	case containsAny(l, "brightness down", "dimmer", "decrease brightness", "lower brightness"):
		action = "brightness_down"
	default:
		return nil, false
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("system_control", map[string]string{"action": action})},
		Confidence: 0.9, Reason: "system phrase",
	}, true
}

type WindowMatcher struct{}

func (WindowMatcher) Name() string { return "window" }
func (WindowMatcher) Match(in NormalizedInput) (*Match, bool) {
	l := in.Lower
	var action string
	switch {
	case containsAny(l, "snap left", "dock left"):
		action = "snap_left"
	case containsAny(l, "snap right", "dock right"):
		action = "snap_right"
	case containsAny(l, "minimize", "minimise"):
		action = "minimize"
	case containsAny(l, "maximize", "maximise", "full screen this"):
		action = "maximize"
	case containsAny(l, "close window", "close this window"):
		action = "close"
	case containsAny(l, "switch window", "switch app", "alt tab"):
		action = "switch"
	default:
		return nil, false
	}
	return &Match{
		Tasks:      []agent.Task{taskJSON("window_control", map[string]string{"action": action})},
		Confidence: 0.9, Reason: "window phrase",
	}, true
}

// Default wires all seven matchers backed by real OS/index lookups, in priority order.
func Default() *Resolver {
	appLookup := func(q string) (string, int) {
		return executor.FindAppMatches(q)
	}
	fileSearch := func(q string) []string {
		recs := search.SearchFiles(q)
		paths := make([]string, 0, len(recs))
		for _, r := range recs {
			paths = append(paths, r.Path)
		}
		return paths
	}
	return NewResolver(
		DateTimeMatcher{},
		MediaMatcher{},
		SystemMatcher{},
		WindowMatcher{},
		WebMatcher{},
		FileMatcher{Search: fileSearch},
		AppMatcher{Lookup: appLookup},
		// Last resort: natural "open my X" phrasing, resolved locally against the
		// file index. Only fires on a confident hit, so it never steals an app
		// launch or opens the wrong file — but it keeps common file opens OFFLINE
		// instead of burning a cloud call (and failing when the quota is spent).
		NaturalFileMatcher{Resolve: search.Resolve},
	)
}
