//go:build whisper

package wakeword

/*
#cgo windows LDFLAGS: -Wl,-Bdynamic
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"

	"github.com/yourname/voice-agent/internal/audio"
)

type kwsEngine struct {
	spotter  *sherpa.KeywordSpotter
	keywords string // the armed phrase line (from keywords.txt)
}

// NewKWS loads the KWS transducer from modelDir and selects the armed phrase.
// Returns (nil, err) when any required file is missing so main degrades cleanly.
func NewKWS(modelDir, wakeWord string) (WakeEngine, error) {
	enc := filepath.Join(modelDir, "encoder.onnx")
	dec := filepath.Join(modelDir, "decoder.onnx")
	join := filepath.Join(modelDir, "joiner.onnx")
	tokens := filepath.Join(modelDir, "tokens.txt")
	kwFile := filepath.Join(modelDir, "keywords.txt")
	for _, p := range []string{enc, dec, join, tokens, kwFile} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("kws model file missing: %s", p)
		}
	}

	kwBytes, err := os.ReadFile(kwFile)
	if err != nil {
		return nil, err
	}
	armed, ok := selectKeyword(string(kwBytes), wakeWord)
	if !ok {
		if armed, ok = firstKeyword(string(kwBytes)); !ok {
			return nil, fmt.Errorf("kws keywords.txt has no usable phrase")
		}
	}

	cfg := sherpa.KeywordSpotterConfig{
		FeatConfig: sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80},
		ModelConfig: sherpa.OnlineModelConfig{
			Transducer: sherpa.OnlineTransducerModelConfig{Encoder: enc, Decoder: dec, Joiner: join},
			Tokens:     tokens,
			NumThreads: 1,
			Provider:   "cpu",
		},
		KeywordsFile:      kwFile,
		KeywordsScore:     1.0,
		KeywordsThreshold: 0.25,
		MaxActivePaths:    4,
	}
	spotter := sherpa.NewKeywordSpotter(&cfg)
	if spotter == nil {
		return nil, fmt.Errorf("kws: NewKeywordSpotter returned nil (bad model?)")
	}
	return &kwsEngine{spotter: spotter, keywords: armed}, nil
}

func (e *kwsEngine) Close() {
	if e.spotter != nil {
		sherpa.DeleteKeywordSpotter(e.spotter)
		e.spotter = nil
	}
}

// kwsDetector adapts one armed OnlineStream to the Detector interface.
type kwsDetector struct {
	spotter *sherpa.KeywordSpotter
	stream  *sherpa.OnlineStream
	f32     []float32
}

func (e *kwsEngine) newDetector() *kwsDetector {
	return &kwsDetector{
		spotter: e.spotter,
		stream:  sherpa.NewKeywordStreamWithKeywords(e.spotter, e.keywords),
	}
}

func (d *kwsDetector) Process(frame []int16) (int, error) {
	if cap(d.f32) < len(frame) {
		d.f32 = make([]float32, len(frame))
	}
	d.f32 = d.f32[:len(frame)]
	for i, s := range frame {
		d.f32[i] = float32(s) / 32768.0
	}
	d.stream.AcceptWaveform(16000, d.f32)
	for d.spotter.IsReady(d.stream) {
		d.spotter.Decode(d.stream)
	}
	if d.spotter.GetResult(d.stream).Keyword != "" {
		d.spotter.Reset(d.stream)
		return 0, nil
	}
	return -1, nil
}

func (e *kwsEngine) StartLoop(ctx context.Context, src FrameSource, onDetect func(), isBusy func() bool) error {
	return runWakeLoop(ctx, src, e.newDetector(), onDetect, isBusy)
}

// kwsHearer feeds the barge-in path the same engine on its own stream.
type kwsHearer struct{ det *kwsDetector }

func (h *kwsHearer) Hear(frame []int16) bool {
	idx, err := h.det.Process(frame)
	return err == nil && idx >= 0
}

func (e *kwsEngine) Hearer() audio.KeywordHearer { return &kwsHearer{det: e.newDetector()} }
