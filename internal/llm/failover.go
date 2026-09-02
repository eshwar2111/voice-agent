package llm

import (
	"context"
	"log"
)

// FallbackProvider wraps a primary Provider and, when a call fails (a rate-limit
// 429, an outage, a bad key), transparently retries on a secondary provider.
// This is what makes a single provider's quota wall (e.g. Gemini free-tier
// 20/day) non-fatal: configure fallback_provider + fallback_api_key and the
// agent keeps working on the backup.
type FallbackProvider struct {
	primary  Provider
	fallback Provider
}

func NewFallbackProvider(primary, fallback Provider) *FallbackProvider {
	return &FallbackProvider{primary: primary, fallback: fallback}
}

func (f *FallbackProvider) GenerateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error) {
	out, err := f.primary.GenerateIntent(ctx, req)
	if err != nil {
		log.Printf("[llm] primary failed (%v) — falling back", err)
		return f.fallback.GenerateIntent(ctx, req)
	}
	return out, nil
}

func (f *FallbackProvider) StreamGenerateIntent(ctx context.Context, req IntentRequest, ch chan<- string) (IntentResponse, error) {
	out, err := f.primary.StreamGenerateIntent(ctx, req, ch)
	if err != nil {
		log.Printf("[llm] primary stream failed (%v) — falling back", err)
		return f.fallback.StreamGenerateIntent(ctx, req, ch)
	}
	return out, nil
}

func (f *FallbackProvider) ClassifyAndPlan(ctx context.Context, transcript, toolSchemas, systemContext string) (ClassifyResponse, error) {
	out, err := f.primary.ClassifyAndPlan(ctx, transcript, toolSchemas, systemContext)
	if err != nil {
		log.Printf("[llm] primary classify failed (%v) — falling back", err)
		return f.fallback.ClassifyAndPlan(ctx, transcript, toolSchemas, systemContext)
	}
	return out, nil
}

func (f *FallbackProvider) Generate(ctx context.Context, prompt string, images [][]byte) (string, error) {
	out, err := f.primary.Generate(ctx, prompt, images)
	if err != nil {
		log.Printf("[llm] primary generate failed (%v) — falling back", err)
		return f.fallback.Generate(ctx, prompt, images)
	}
	return out, nil
}
