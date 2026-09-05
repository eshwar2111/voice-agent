package llm

import "context"

type IntentRequest struct {
	UserText      string // The whisper-transcribed text from the user's voice input
	SystemContext string
	ToolSchemas   string
	Image         []byte // Screenshot for visual context
	// Audio is intentionally removed — local Whisper handles transcription before this point
}

type IntentResponse struct {
	RawJSON string
}

// ClassifyResponse is the fast-path classification result.
// If NeedsScreen is false, RawJSON contains the full task plan ready to execute.
type ClassifyResponse struct {
	NeedsScreen bool
	RawJSON     string // populated when NeedsScreen is false
}

type Provider interface {
	GenerateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error)
	StreamGenerateIntent(ctx context.Context, req IntentRequest, textDeltaChan chan<- string) (IntentResponse, error)
	// ClassifyAndPlan does a fast text-only classification. If the command doesn't
	// need screen context, it returns the full plan in RawJSON directly.
	ClassifyAndPlan(ctx context.Context, transcript, toolSchemas, systemContext string) (ClassifyResponse, error)
	Generate(ctx context.Context, prompt string, images [][]byte) (string, error)

	// StreamGenerate is Generate with progressive delivery: it sends text deltas
	// to ch as they arrive and returns the full accumulated answer. Implementers
	// that don't token-stream MUST still honor the contract by emitting the whole
	// answer as one delta. The callee ALWAYS closes ch (including on error), so a
	// caller can range over it safely. A nil ch means "don't stream" (equivalent
	// to Generate).
	StreamGenerate(ctx context.Context, prompt string, images [][]byte, ch chan<- string) (string, error)
}

// streamViaGenerate adapts a non-streaming Generate into the StreamGenerate
// contract: run it, emit the whole answer as a single delta, and close ch.
func streamViaGenerate(gen func() (string, error), ch chan<- string) (string, error) {
	out, err := gen()
	if ch != nil {
		if err == nil && out != "" {
			ch <- out
		}
		close(ch)
	}
	return out, err
}
