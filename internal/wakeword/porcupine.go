//go:build whisper

package wakeword

import (
	"context"
	"fmt"

	porcupine "github.com/Picovoice/porcupine/binding/go/v3"
	pvrecorder "github.com/Picovoice/pvrecorder/binding/go"
)

type pvSource struct{ rec *pvrecorder.PvRecorder }

func (s *pvSource) Read() ([]int16, error) { return s.rec.Read() }
func (s *pvSource) Start() error           { return s.rec.Start() }
func (s *pvSource) Stop() error            { s.rec.Stop(); return nil }

type ppDetector struct{ p *porcupine.Porcupine }

func (d *ppDetector) Process(f []int16) (int, error) { return d.p.Process(f) }

// StartWakeWordLoop listens for the built-in "Porcupine" keyword until ctx is cancelled.
func StartWakeWordLoop(ctx context.Context, accessKey string, onDetect func(), isBusy func() bool) error {
	p := porcupine.Porcupine{
		AccessKey:       accessKey,
		BuiltInKeywords: []porcupine.BuiltInKeyword{porcupine.PORCUPINE},
	}
	if err := p.Init(); err != nil {
		return fmt.Errorf("porcupine init: %w", err)
	}
	defer p.Delete()

	rec := pvrecorder.NewPvRecorder(porcupine.FrameLength)
	rec.DeviceIndex = -1
	if err := rec.Init(); err != nil {
		return fmt.Errorf("pvrecorder init: %w", err)
	}
	defer rec.Delete()
	if err := rec.Start(); err != nil {
		return fmt.Errorf("pvrecorder start: %w", err)
	}
	fmt.Println("🎙️  Wake word active — say 'Porcupine'")
	return runWakeLoop(ctx, &pvSource{&rec}, &ppDetector{&p}, onDetect, isBusy)
}
