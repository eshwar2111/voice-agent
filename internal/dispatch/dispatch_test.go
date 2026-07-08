package dispatch

import (
	"context"
	"encoding/json"
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
