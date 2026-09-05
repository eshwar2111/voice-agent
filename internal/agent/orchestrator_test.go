package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/yourname/voice-agent/internal/llm"
)

type capturingProvider struct{ lastPrompt string }

func (p *capturingProvider) GenerateIntent(context.Context, llm.IntentRequest) (llm.IntentResponse, error) {
	return llm.IntentResponse{}, nil
}
func (p *capturingProvider) StreamGenerateIntent(context.Context, llm.IntentRequest, chan<- string) (llm.IntentResponse, error) {
	return llm.IntentResponse{}, nil
}
func (p *capturingProvider) ClassifyAndPlan(context.Context, string, string, string) (llm.ClassifyResponse, error) {
	return llm.ClassifyResponse{}, nil
}
func (p *capturingProvider) Generate(_ context.Context, prompt string, _ [][]byte) (string, error) {
	p.lastPrompt = prompt
	return "[]", nil // empty decompose -> orchestrator falls back, but prompt is captured
}

func (p *capturingProvider) StreamGenerate(_ context.Context, prompt string, _ [][]byte, ch chan<- string) (string, error) {
	p.lastPrompt = prompt
	if ch != nil {
		ch <- "[]"
		close(ch)
	}
	return "[]", nil
}

func TestRunForwardsSysContextToDecompose(t *testing.T) {
	p := &capturingProvider{}
	orch := NewOrchestrator(p, NewExecutor(nil))
	// Empty decompose result makes execSubGoal run google_workflow_agent against a nil
	// registry -> ExecutePlan errors (or panics on the nil registry, which we recover
	// from here since we only care about the decompose prompt); we only assert the
	// decompose prompt carried context.
	func() {
		defer func() { _ = recover() }()
		_ = orch.Run(context.Background(), "reply to this", "Selected Text: hello world")
	}()
	if !strings.Contains(p.lastPrompt, "hello world") {
		t.Errorf("decompose prompt missing sysContext; got: %s", p.lastPrompt)
	}
}
