package llm

import (
	"context"
	"errors"
	"sync"
)

// ProxyProvider is a thread-safe wrapper that allows the underlying LLM Provider
// to be swapped dynamically at runtime (e.g., via Settings or 1st-time onboarding)
type ProxyProvider struct {
	mu       sync.RWMutex
	provider Provider
}

func NewProxyProvider(initial Provider) *ProxyProvider {
	return &ProxyProvider{provider: initial}
}

func (p *ProxyProvider) SetProvider(newProvider Provider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.provider = newProvider
}

// GenerateIntent forwards the request or returns an error if unset
func (p *ProxyProvider) GenerateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.provider == nil {
		return IntentResponse{}, errors.New("LLM not configured. Please complete setup in Settings.")
	}
	return p.provider.GenerateIntent(ctx, req)
}

func (p *ProxyProvider) StreamGenerateIntent(ctx context.Context, req IntentRequest, textDeltaChan chan<- string) (IntentResponse, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.provider == nil {
		return IntentResponse{}, errors.New("LLM not configured. Please complete setup in Settings.")
	}
	return p.provider.StreamGenerateIntent(ctx, req, textDeltaChan)
}

func (p *ProxyProvider) ClassifyAndPlan(ctx context.Context, transcript, toolSchemas, systemContext string) (ClassifyResponse, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.provider == nil {
		return ClassifyResponse{NeedsScreen: true}, errors.New("LLM not configured. Please complete setup in Settings.")
	}
	return p.provider.ClassifyAndPlan(ctx, transcript, toolSchemas, systemContext)
}

func (p *ProxyProvider) Generate(ctx context.Context, prompt string, images [][]byte) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.provider == nil {
		return "", errors.New("LLM not configured. Please complete setup in Settings.")
	}
	return p.provider.Generate(ctx, prompt, images)
}
