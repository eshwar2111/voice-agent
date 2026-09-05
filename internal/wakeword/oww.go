package wakeword

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/yourname/voice-agent/internal/audio"
)

// This file is a faithful Go port of openWakeWord's streaming feature pipeline
// (github.com/dscripka/openWakeWord — openwakeword/utils.py AudioFeatures). It
// runs three ONNX models in sequence via onnxruntime (the same runtime BGE
// uses): raw 16 kHz audio -> melspectrogram (32 mel bins) -> Google
// speech-embedding (96-dim) -> a per-wake-word classifier probability. It
// replaces sherpa KWS for wake detection because openWakeWord ships models
// trained specifically for phrases like "hey jarvis", giving far better recall
// than an ASR-token KWS on a rare proper noun. Verified numerically identical to
// the Python reference (max 0.998 on the same clip) in oww_test.go.

var owwOrtOnce sync.Once
var owwOrtErr error

// owwEnsureORT initializes the shared onnxruntime environment exactly once. It
// is safe alongside the BGE embedder (which also uses onnxruntime): the
// IsInitialized guard means whichever runs first wins and the other is a no-op.
// An explicit ONNXRUNTIME_LIB_PATH pins the DLL (avoids a stray system copy).
func owwEnsureORT() error {
	owwOrtOnce.Do(func() {
		if ort.IsInitialized() {
			return
		}
		if p := os.Getenv("ONNXRUNTIME_LIB_PATH"); p != "" {
			ort.SetSharedLibraryPath(p)
		}
		owwOrtErr = ort.InitializeEnvironment()
	})
	return owwOrtErr
}

const (
	owwChunk     = 1280    // 80 ms @16 kHz — openWakeWord's processing unit
	owwMelWindow = 76      // mel frames per embedding window
	owwMelStep   = 8       // embedding stride, in mel frames
	owwMelMax    = 10 * 97 // rolling melspectrogram cap (~10 s)
	owwFeatN     = 16      // embeddings fed to the wake classifier
	owwFeatMax   = 120     // rolling embedding cap
	owwRawMax    = 16000 * 10
	owwMelCtx    = 480 // extra audio context fed to the melspec model (160*3)
	owwThreshold = 0.5 // openWakeWord's default detection threshold
)

// owwModels maps a spoken wake word to its openWakeWord classifier file. The
// melspec + embedding models are shared across all of them.
var owwModels = map[string]string{
	"hey jarvis":  "hey_jarvis_v0.1.onnx",
	"jarvis":      "hey_jarvis_v0.1.onnx",
	"alexa":       "alexa_v0.1.onnx",
	"hey mycroft": "hey_mycroft_v0.1.onnx",
	"hey rhasspy": "hey_rhasspy_v0.1.onnx",
}

// owwEngine holds the three loaded (stateless) ONNX sessions, shared by every
// detector. Per-stream buffers live in owwDetector.
type owwEngine struct {
	melspec, embed, wake *ort.DynamicAdvancedSession
	threshold            float32
}

func newOWWEngine(melPath, embedPath, wakePath string, threshold float32) (*owwEngine, error) {
	if err := owwEnsureORT(); err != nil {
		return nil, err
	}
	ms, err := ort.NewDynamicAdvancedSession(melPath, []string{"input"}, []string{"output"}, nil)
	if err != nil {
		return nil, fmt.Errorf("melspec model: %w", err)
	}
	es, err := ort.NewDynamicAdvancedSession(embedPath, []string{"input_1"}, []string{"conv2d_19"}, nil)
	if err != nil {
		ms.Destroy()
		return nil, fmt.Errorf("embedding model: %w", err)
	}
	ws, err := ort.NewDynamicAdvancedSession(wakePath, []string{"x.1"}, []string{"53"}, nil)
	if err != nil {
		ms.Destroy()
		es.Destroy()
		return nil, fmt.Errorf("wake model: %w", err)
	}
	return &owwEngine{melspec: ms, embed: es, wake: ws, threshold: threshold}, nil
}

func (e *owwEngine) Close() {
	for _, s := range []*ort.DynamicAdvancedSession{e.melspec, e.embed, e.wake} {
		if s != nil {
			s.Destroy()
		}
	}
	e.melspec, e.embed, e.wake = nil, nil, nil
}

// owwDetector is one streaming detector (its own audio/mel/feature buffers over
// the engine's shared sessions).
type owwDetector struct {
	eng    *owwEngine
	raw    []int16
	rawRem []int16
	mel    [][32]float32
	feat   [][96]float32
	accum  int
}

// newDetector returns a detector seeded the way openWakeWord does: the
// melspectrogram buffer starts as 76 rows of ones and the feature buffer starts
// with 16 rows (so the classifier can run immediately, reading ~0 until real
// audio fills in).
func (e *owwEngine) newDetector() *owwDetector {
	d := &owwDetector{eng: e}
	d.mel = make([][32]float32, owwMelWindow)
	for i := range d.mel {
		for j := range d.mel[i] {
			d.mel[i][j] = 1.0
		}
	}
	d.feat = make([][96]float32, owwFeatN)
	return d
}

func (d *owwDetector) getMelspec(x []int16) ([][32]float32, error) {
	xf := make([]float32, len(x))
	for i, s := range x {
		xf[i] = float32(s)
	}
	in, err := ort.NewTensor(ort.NewShape(1, int64(len(x))), xf)
	if err != nil {
		return nil, err
	}
	defer in.Destroy()
	outs := []ort.Value{nil} // nil => onnxruntime allocates the (dynamic) output
	if err := d.eng.melspec.Run([]ort.Value{in}, outs); err != nil {
		return nil, err
	}
	defer outs[0].Destroy()
	data := outs[0].(*ort.Tensor[float32]).GetData() // shape [1,1,frames,32], row-major
	frames := len(data) / 32
	res := make([][32]float32, frames)
	for f := 0; f < frames; f++ {
		for j := 0; j < 32; j++ {
			res[f][j] = data[f*32+j]/10.0 + 2.0 // openWakeWord's melspec transform
		}
	}
	return res, nil
}

func (d *owwDetector) getEmbedding(win [][32]float32) ([96]float32, error) {
	var e [96]float32
	xf := make([]float32, owwMelWindow*32)
	for f := 0; f < owwMelWindow; f++ {
		for j := 0; j < 32; j++ {
			xf[f*32+j] = win[f][j]
		}
	}
	in, err := ort.NewTensor(ort.NewShape(1, owwMelWindow, 32, 1), xf)
	if err != nil {
		return e, err
	}
	defer in.Destroy()
	outs := []ort.Value{nil}
	if err := d.eng.embed.Run([]ort.Value{in}, outs); err != nil {
		return e, err
	}
	defer outs[0].Destroy()
	copy(e[:], outs[0].(*ort.Tensor[float32]).GetData())
	return e, nil
}

func (d *owwDetector) bufferRaw(x []int16) {
	d.raw = append(d.raw, x...)
	if len(d.raw) > owwRawMax {
		d.raw = d.raw[len(d.raw)-owwRawMax:]
	}
}

func (d *owwDetector) streamingMelspec(nSamples int) error {
	if len(d.raw) < 400 {
		return nil
	}
	start := len(d.raw) - nSamples - owwMelCtx
	if start < 0 {
		start = 0
	}
	m, err := d.getMelspec(d.raw[start:])
	if err != nil {
		return err
	}
	d.mel = append(d.mel, m...)
	if len(d.mel) > owwMelMax {
		d.mel = d.mel[len(d.mel)-owwMelMax:]
	}
	return nil
}

// streamingFeatures ports AudioFeatures._streaming_features: buffer raw audio
// into whole 80 ms chunks, and on each chunk boundary compute new melspectrogram
// frames and the resulting embedding(s).
func (d *owwDetector) streamingFeatures(x []int16) error {
	if len(d.rawRem) > 0 {
		x = append(append([]int16(nil), d.rawRem...), x...)
		d.rawRem = nil
	}
	if d.accum+len(x) >= owwChunk {
		remainder := (d.accum + len(x)) % owwChunk
		if remainder != 0 {
			even := x[:len(x)-remainder]
			d.bufferRaw(even)
			d.accum += len(even)
			d.rawRem = append([]int16(nil), x[len(x)-remainder:]...)
		} else {
			d.bufferRaw(x)
			d.accum += len(x)
			d.rawRem = nil
		}
	} else {
		d.accum += len(x)
		d.bufferRaw(x)
	}

	if d.accum >= owwChunk && d.accum%owwChunk == 0 {
		if err := d.streamingMelspec(d.accum); err != nil {
			return err
		}
		// One embedding per new 80 ms chunk. Python uses mel[-76+ndx:ndx] with
		// ndx=-8*i; hi=n-8i, lo=hi-76 is the same slice for every i (i=0 => the
		// last 76 frames), without Python's ndx==0 special case.
		for i := d.accum/owwChunk - 1; i >= 0; i-- {
			n := len(d.mel)
			hi := n - owwMelStep*i
			lo := hi - owwMelWindow
			if lo >= 0 && hi <= n {
				e, err := d.getEmbedding(d.mel[lo:hi])
				if err != nil {
					return err
				}
				d.feat = append(d.feat, e)
			}
		}
		d.accum = 0
	}

	if len(d.feat) > owwFeatMax {
		d.feat = d.feat[len(d.feat)-owwFeatMax:]
	}
	return nil
}

// predict feeds a chunk of audio and returns the current wake-word probability.
func (d *owwDetector) predict(chunk []int16) (float32, error) {
	if err := d.streamingFeatures(chunk); err != nil {
		return 0, err
	}
	if len(d.feat) < owwFeatN {
		return 0, nil
	}
	xf := make([]float32, owwFeatN*96)
	base := len(d.feat) - owwFeatN
	for r := 0; r < owwFeatN; r++ {
		copy(xf[r*96:(r+1)*96], d.feat[base+r][:])
	}
	in, err := ort.NewTensor(ort.NewShape(1, owwFeatN, 96), xf)
	if err != nil {
		return 0, err
	}
	defer in.Destroy()
	outs := []ort.Value{nil}
	if err := d.eng.wake.Run([]ort.Value{in}, outs); err != nil {
		return 0, err
	}
	defer outs[0].Destroy()
	return outs[0].(*ort.Tensor[float32]).GetData()[0], nil
}

// Process implements Detector: feed one frame, return a hit (>=0) when the
// probability crosses the threshold. On a hit it clears the feature buffer so
// the same utterance can't immediately re-fire.
func (d *owwDetector) Process(frame []int16) (int, error) {
	p, err := d.predict(frame)
	if err != nil {
		return -1, err
	}
	if p >= d.eng.threshold {
		d.feat = d.feat[:0]
		for i := 0; i < owwFeatN; i++ {
			d.feat = append(d.feat, [96]float32{})
		}
		return 0, nil
	}
	return -1, nil
}

// owwWake adapts the engine to the WakeEngine interface used by main.go.
type owwWake struct{ eng *owwEngine }

func (w *owwWake) StartLoop(ctx context.Context, src FrameSource, onDetect func(), isBusy func() bool) error {
	// runWakeLoop assumes the source is already started.
	if err := src.Start(); err != nil {
		return fmt.Errorf("wake: mic source start: %w", err)
	}
	return runWakeLoop(ctx, src, w.eng.newDetector(), onDetect, isBusy)
}

func (w *owwWake) Hearer() audio.KeywordHearer { return &owwHearer{det: w.eng.newDetector()} }
func (w *owwWake) Close()                      { w.eng.Close() }

type owwHearer struct{ det *owwDetector }

func (h *owwHearer) Hear(frame []int16) bool {
	idx, err := h.det.Process(frame)
	return err == nil && idx >= 0
}

// NewKWS loads the openWakeWord models from modelDir and selects the classifier
// for wakeWord. melspectrogram.onnx + embedding_model.onnx are shared; the
// wake-word .onnx is chosen by owwModels (default: hey_jarvis). Returns
// (nil, err) when a model file is missing so the caller degrades cleanly.
func NewKWS(modelDir, wakeWord string) (WakeEngine, error) {
	key := strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(wakeWord, "_", " "))), " ")
	modelFile, ok := owwModels[key]
	if !ok {
		modelFile = "hey_jarvis_v0.1.onnx"
	}
	mel := filepath.Join(modelDir, "melspectrogram.onnx")
	emb := filepath.Join(modelDir, "embedding_model.onnx")
	wake := filepath.Join(modelDir, modelFile)
	for _, p := range []string{mel, emb, wake} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("openWakeWord model missing: %s", p)
		}
	}
	eng, err := newOWWEngine(mel, emb, wake, owwThreshold)
	if err != nil {
		return nil, err
	}
	return &owwWake{eng: eng}, nil
}
