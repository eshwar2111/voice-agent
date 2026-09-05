# Wake-Word Swap: Porcupine → sherpa-onnx KWS — Design Spec

**Date:** 2026-09-05
**Status:** Approved for planning
**Author:** Eshwar B (with Claude)

## Goal

Replace the Porcupine (Picovoice) wake word with a fully local, key-free
**sherpa-onnx Keyword Spotting (KWS)** engine, and remove the Picovoice
dependency entirely (both `porcupine` and `pvrecorder`). The same KWS engine
also powers the barge-in interrupt added in the voice-feel pass. This delivers
the product promise: **install → works offline → no account, no API key**.

## Why

`porcupine.go` requires a Picovoice **access key** baked into the build (a free
account, redistribution-licensing friction, and a config secret). Its inference
is local, but the account/key dependency contradicts "install and it just
works." sherpa-onnx KWS is Apache-2.0, offline, keyless, has an official Go
binding, and a **spike on 2026-09-05 proved it links and runs with the project's
w64devkit gcc 14.1.0 toolchain** (target triple `x86_64-pc-windows-gnu`).

## Global Constraints

- **Keyless / offline:** no account, no network call, no embedded secret. Ever.
- **Nil-safe degradation:** if the KWS model is absent or fails to load, the wake
  word disables cleanly (log once) — the pill, typing, command capture, and
  open-mic barge-in all keep working. Mirrors the BGE semantic-search pattern.
- **No new config secret;** `porcupine_access_key` is deprecated (ignored).
- **Single mic runtime:** capture unifies on `malgo` (Picovoice removed).
- **Build:** voice build stays `-tags "whisper sqlite_fts5"`; KWS code lives
  under the `whisper` tag with a non-whisper stub, so typed-only builds are
  unaffected and need no sherpa DLLs.
- Commit trailers: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` and
  `Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr`.
- Explicit `git add` only; never commit `config.json` or model binaries.

## Dependency & Build

- Add `github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx` (v1.13.7). It links against
  `sherpa-onnx-c-api.dll` (4.4 MB) + `onnxruntime.dll` (17 MB) via CGO, GNU ABI.
- These DLLs must ship next to `voice-agent.exe`.
- **`onnxruntime.dll` reconciliation (RISK — verify first):** the project already
  ships an `onnxruntime.dll` for BGE (`onnxruntime_go`, loaded at runtime by
  path). sherpa links its own at build. One `onnxruntime.dll` on disk must serve
  both, or versions must be compatible. Task 0 verifies this and the combined
  whisper.cpp(static) + sherpa(dynamic) build before any feature work.

## Architecture

The `internal/wakeword/loop.go` abstraction is engine-agnostic and **unchanged**:

```
runWakeLoop(ctx, FrameSource, Detector, onDetect, isBusy)
  FrameSource: Read() []int16 / Start() / Stop()   (mic baton)
  Detector:    Process(frame []int16) (int, error) (>=0 => keyword hit)
```

We swap the two concrete implementations behind it and share one loaded engine:

```
                 ┌──────────────────────────────┐
                 │  KWS (one *KeywordSpotter)    │  built once in main
                 │  loaded from models/kws/      │
                 └───────┬───────────────┬───────┘
                         │               │
             NewStream() │               │ Hearer() (audio.KeywordHearer)
                         ▼               ▼
        idle wake loop (malgoSource)   barge-in (WatchForBargeIn)
        runWakeLoop + kwsDetector      shares the spotter, own stream
```

## Components

### 1. `internal/wakeword/kws.go` (new; `//go:build whisper`)

Replaces `porcupine.go`. Owns the sherpa engine and adapts it to `Detector`.

```go
type KWS struct {
    spotter *sherpa.KeywordSpotter
    keywords string // the active, pre-tokenized phrase (from keywords.txt @label)
}

// NewKWS loads the spotter from modelDir (encoder/decoder/joiner.onnx, tokens.txt,
// keywords.txt) and selects the active phrase whose @label == wakeWord. Returns
// (nil, err) if the model is absent/unloadable so main degrades cleanly.
func NewKWS(modelDir, wakeWord string) (*KWS, error)

func (k *KWS) Close()

// kwsDetector implements Detector over one OnlineStream armed with k.keywords.
type kwsDetector struct { spotter *sherpa.KeywordSpotter; stream *sherpa.OnlineStream }

func (k *KWS) newDetector() *kwsDetector // NewKeywordStreamWithKeywords(spotter, k.keywords)

func (d *kwsDetector) Process(frame []int16) (int, error) {
    // int16 -> float32; stream.AcceptWaveform(16000, f32)
    // for spotter.IsReady(stream) { spotter.Decode(stream) }
    // if spotter.GetResult(stream).Keyword != "" { spotter.Reset(stream); return 0, nil }
    // return -1, nil
}
```

`StartWakeWordLoop` is re-cut to take a `*KWS` + a `FrameSource`, no access key:

```go
func StartWakeWordLoop(ctx context.Context, kws *KWS, src FrameSource,
    onDetect func(), isBusy func() bool) error {
    return runWakeLoop(ctx, src, kws.newDetector(), onDetect, isBusy)
}
```

### 2. malgo `FrameSource` (new; `internal/audio` or `internal/wakeword`)

Replaces `pvSource` (pvrecorder). A callback-fed ring buffer with a blocking,
fixed-size pull `Read()` so it satisfies `FrameSource` while `malgo` is
push-based. Reuses the capture config already in `internal/audio/mic.go`
(16 kHz mono F32). `Start()` opens the device; `Stop()` closes it (the mic baton
still guarantees only one recorder at a time).

```go
type MicSource struct { /* malgo device + ring buffer + frameLen */ }
func NewMicSource(frameLen int) *MicSource
func (m *MicSource) Start() error
func (m *MicSource) Stop() error
func (m *MicSource) Read() ([]int16, error) // blocks until frameLen samples
```

### 3. Barge-in hearer (`internal/wakeword/hearer.go`, updated)

`NewKeywordHearer` becomes sherpa-backed and takes the shared `*KWS`:

```go
func (k *KWS) Hearer() audio.KeywordHearer // wraps a fresh KWS stream
// Hear(frame []int16) bool: AcceptWaveform + drain + GetResult.Keyword != ""
```

`audio.WatchForBargeIn` is unchanged (already accepts an `audio.KeywordHearer`).
`hearer_stub.go` (non-whisper) still returns nil.

### 4. Config (`config/config.go`)

- **Deprecate** `PorcupineAccessKey` — keep the field for backward compat, ignore
  it, log one line if a non-empty value is present.
- **Add:**
  - `WakeWord string json:"wake_word"` — the selected phrase label; default
    `"hey jarvis"` (via absent-vs-set detection; must match a `keywords.txt`
    `@label`).
  - `WakeWordEnabled bool json:"wake_word_enabled"` — defaults **true** when
    absent (same raw-map trick as `trusted_execution`).
  - `KWSModelPath string json:"kws_model_path"` — default `models/kws/`.
- `DefaultKWSModelPath = "models/kws/"`; curated `DefaultWakeWord = "hey jarvis"`.

### 5. Model bundling & curated phrases

```
models/kws/
  encoder.onnx  decoder.onnx  joiner.onnx   (sherpa gigaspeech KWS transducer)
  tokens.txt
  keywords.txt   (curated, PRE-TOKENIZED, one phrase per line with @label)
```

`keywords.txt` (checked in — text, not a binary) holds the curated set, each line
already tokenized for the bundled model with an `@label`, e.g.:

```
▁HE ▁Y ▁JAR ▁VIS @hey jarvis
▁COM ▁PU ▁TER @computer
▁HE ▁Y ▁NO ▁VA @hey nova
▁HE ▁LLO ▁FRI ▁DAY @hello friday
```

`NewKWS` selects the line whose `@label == wakeWord` and arms only that phrase via
`NewKeywordStreamWithKeywords`. Unknown/empty → first line.
`.onnx`/`tokens.txt` are gitignored (fetched per `docs/BUILD-VOICE.md`, like BGE).

### 6. Wiring (`cmd/app/main.go`)

```go
var kws *wakeword.KWS
if cfg.EnableVoice && cfg.WakeWordEnabled {
    if k, err := wakeword.NewKWS(cfg.KWSModelPath, cfg.WakeWord); err == nil {
        kws = k; defer kws.Close()
    } else {
        log.Printf("[wake] KWS unavailable: %v (wake word off; pill + typing still work)", err)
    }
}
// barge-in hearer now sherpa-backed (replaces Porcupine hearer):
var kwHearer audio.KeywordHearer
if kws != nil { kwHearer = kws.Hearer() }
executor.BargeWatch = func(ctx, stop) { audio.WatchForBargeIn(ctx, kwHearer, stop) }

// idle wake loop on the malgo source:
if kws != nil {
    go func() {
        src := audio.NewMicSource(kwsFrameLen)
        busy := func() bool { return engineApp.IsBusy() || executor.IsSpeaking() }
        if err := wakeword.StartWakeWordLoop(rootCtx, kws, src,
            func(){ engineApp.TriggerAndWait(60*time.Second) }, busy); err != nil {
            log.Printf("wake word stopped: %v", err)
        }
    }()
}
```

The `IsSpeaking()`-as-busy gate (added in the barge-in commit) is retained so the
wake loop yields the mic to the barge watcher during TTS.

## Degradation matrix

| Condition | Behavior |
|---|---|
| `models/kws/` present, loads | Wake word + keyword barge-in fully active |
| Model absent / load fails | Wake word off; open-mic barge-in still works; pill + typing work |
| `wake_word_enabled=false` | No wake loop; everything else works |
| `enable_voice=false` | No mic paths at all (unchanged) |
| Old config with `porcupine_access_key` | Ignored + one-line notice; no behavior change |

## Testing

- `loop_test.go` (fake `FrameSource`/`Detector`) — **unchanged**, still guards the
  baton logic.
- New `MicSource` unit test: push synthetic callback buffers, assert `Read()`
  returns fixed-size int16 frames in order (no sherpa/model needed).
- KWS parse test: `keywords.txt` line → `@label` selection logic (pure, no model).
- Integration test (gated on `models/kws/` presence, skipped in CI): construct
  `NewKWS`, feed a known wake-word WAV, assert a hit; feed noise, assert none.
- Full `go test -tags "whisper sqlite_fts5" ./...` green; both build configs.

## Out of scope (later)

- **Arbitrary user-typed wake words** (runtime text→token). MVP is the curated
  pick-list; the `NewKeywordStreamWithKeywords` seam keeps this a later add.
- Replacing STT (Moonshine) / TTS (Kokoro/Kitten) — separate threads.
- The "voice store" installer / model download manager.

## Task 0 (must pass before feature work)

Verify, in the real project (not the isolated spike):
1. `go build -tags "whisper sqlite_fts5"` links whisper.cpp (static) **and**
   sherpa (dynamic) together into `voice-agent.exe`.
2. One `onnxruntime.dll` on disk serves both BGE and sherpa (or pin a compatible
   version). If they conflict, resolve before proceeding.
