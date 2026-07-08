package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yourname/voice-agent/internal/agent"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/resolver"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
)

// recordingProvider fails the test if any provider method is called.
type recordingProvider struct{ called bool }

func (p *recordingProvider) GenerateIntent(context.Context, llm.IntentRequest) (llm.IntentResponse, error) {
	p.called = true
	return llm.IntentResponse{}, nil
}
func (p *recordingProvider) StreamGenerateIntent(context.Context, llm.IntentRequest, chan<- string) (llm.IntentResponse, error) {
	p.called = true
	return llm.IntentResponse{}, nil
}
func (p *recordingProvider) ClassifyAndPlan(context.Context, string, string, string) (llm.ClassifyResponse, error) {
	p.called = true
	return llm.ClassifyResponse{}, nil
}
func (p *recordingProvider) Generate(context.Context, string, [][]byte) (string, error) {
	p.called = true
	return "[]", nil
}

// staticMatcher always returns a get_datetime task above threshold.
type staticMatcher struct{}

func (staticMatcher) Name() string { return "static" }
func (staticMatcher) Match(in resolver.NormalizedInput) (*resolver.Match, bool) {
	return &resolver.Match{
		Tasks:      []agent.Task{{Tool: "get_datetime", Params: json.RawMessage(`{}`)}},
		Confidence: 1.0,
	}, true
}

func TestHandleTier0MakesNoProviderCall(t *testing.T) {
	prov := &recordingProvider{}
	reg := tools.DefaultRegistry(prov) // includes get_datetime
	profile := security.DeveloperProfile()
	d := &Deps{
		Registry: reg,
		Provider: prov,
		Profile:  &profile,
		Resolver: resolver.NewResolver(staticMatcher{}),
	}
	if err := d.Handle(context.Background(), "what time is it", ""); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if prov.called {
		t.Fatal("Tier 0 path must not call the LLM provider")
	}
	if LocalCount() < 1 {
		t.Error("LocalCount should have incremented")
	}
}

// noMatchMatcher never matches, forcing the resolver into a Tier 1 (cloud) miss.
type noMatchMatcher struct{}

func (noMatchMatcher) Name() string { return "no_match" }
func (noMatchMatcher) Match(in resolver.NormalizedInput) (*resolver.Match, bool) {
	return nil, false
}

// disallowedToolMatcher returns a task for a tool that is deliberately left
// out of the profile's allow-list.
type disallowedToolMatcher struct{}

func (disallowedToolMatcher) Name() string { return "disallowed" }
func (disallowedToolMatcher) Match(in resolver.NormalizedInput) (*resolver.Match, bool) {
	return &resolver.Match{
		Tasks:      []agent.Task{{Tool: "get_datetime", Params: json.RawMessage(`{}`)}},
		Confidence: 1.0,
	}, true
}

// unregisteredToolMatcher returns a task for a tool name that exists nowhere
// in the registry, regardless of the profile's allow-list.
type unregisteredToolMatcher struct{}

func (unregisteredToolMatcher) Name() string { return "unregistered" }
func (unregisteredToolMatcher) Match(in resolver.NormalizedInput) (*resolver.Match, bool) {
	return &resolver.Match{
		Tasks:      []agent.Task{{Tool: "no_such_tool", Params: json.RawMessage(`{}`)}},
		Confidence: 1.0,
	}, true
}

func TestHandleRejectsDisallowedTool(t *testing.T) {
	prov := &recordingProvider{}
	reg := tools.DefaultRegistry(prov) // includes get_datetime
	restricted := security.Profile{Name: "restricted", AllowedTools: map[string]bool{}}
	d := &Deps{
		Registry: reg,
		Provider: prov,
		Profile:  &restricted,
		Resolver: resolver.NewResolver(disallowedToolMatcher{}),
	}
	if err := d.Handle(context.Background(), "what time is it", ""); err == nil {
		t.Fatal("expected Handle to reject a tool not in the profile's allow-list")
	}
	if prov.called {
		t.Fatal("provider must not be called when the security check rejects the tool")
	}
}

func TestHandleRejectsUnregisteredTool(t *testing.T) {
	prov := &recordingProvider{}
	reg := tools.DefaultRegistry(prov)
	profile := security.DeveloperProfile()
	profile.AllowedTools["no_such_tool"] = true // allowed by profile, but not registered
	d := &Deps{
		Registry: reg,
		Provider: prov,
		Profile:  &profile,
		Resolver: resolver.NewResolver(unregisteredToolMatcher{}),
	}
	err := d.Handle(context.Background(), "do the thing", "")
	if err == nil {
		t.Fatal("expected Handle to reject an unregistered tool")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("expected error to mention 'not registered', got: %v", err)
	}
}

func TestResolverToolsAllowedByDeveloperProfile(t *testing.T) {
	p := security.DeveloperProfile()
	// The set of tools the Tier-0 resolver (resolver.Default) can emit. Keep in sync
	// with internal/resolver/matchers.go — a Tier-0 match to a tool the active profile
	// disallows hard-fails with no cloud fallback.
	resolverTools := []string{
		"open_app", "open_website", "web_search", "open_file",
		"get_datetime", "media_control", "window_control", "system_control",
	}
	for _, tool := range resolverTools {
		if !p.IsAllowed(tool) {
			t.Errorf("resolver can emit %q but DeveloperProfile disallows it (Tier-0 hard-fail)", tool)
		}
	}
}

func TestHandleTier1IncrementsCloudCount(t *testing.T) {
	prov := &recordingProvider{}
	reg := tools.DefaultRegistry(prov)
	profile := security.DeveloperProfile()
	d := &Deps{
		Registry: reg,
		Provider: prov,
		Profile:  &profile,
		Resolver: resolver.NewResolver(noMatchMatcher{}),
	}
	before := CloudCount()
	_ = d.Handle(context.Background(), "some unmatched free-form request", "")
	after := CloudCount()
	if after != before+1 {
		t.Errorf("expected CloudCount to increment by 1, got before=%d after=%d", before, after)
	}
}
