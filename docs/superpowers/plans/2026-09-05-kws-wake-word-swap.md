# Wake-Word Swap (Porcupine → sherpa-onnx KWS) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Porcupine (Picovoice, account+key) wake word with a local, key-free sherpa-onnx Keyword Spotting engine, remove the Picovoice dependency entirely, and unify microphone capture on malgo.

**Architecture:** The existing `runWakeLoop(ctx, FrameSource, Detector, …)` seam in `internal/wakeword/loop.go` stays. We swap the two concrete implementations behind it: a sherpa-KWS `Detector` and a malgo `FrameSource`. One loaded `KeywordSpotter` is shared (via a `WakeEngine` interface) between the idle wake loop and the barge-in interrupt. Wake words are a curated, pre-tokenized pick-list; missing model → clean degradation.

**Tech Stack:** Go 1.26, CGO (w64devkit gcc 14.1.0), `github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx` v1.13.7 (dynamic DLLs, GNU ABI), `gen2brain/malgo`, SQLite FTS5.

**Spec:** `docs/superpowers/specs/2026-09-05-kws-wake-word-swap-design.md`

## Global Constraints

- Keyless / offline: no account, no network, no embedded secret, ever.
- Nil-safe degradation: KWS model absent/unloadable → wake word off; pill, typing, command capture, and open-mic barge-in all keep working.
- `porcupine_access_key` deprecated (parsed, ignored, one-line notice if non-empty).
- Single mic runtime: capture unifies on `malgo`; Picovoice (`porcupine` + `pvrecorder`) fully removed.
- Voice build stays `-tags "whisper sqlite_fts5"`; KWS/sherpa code lives under the `whisper` tag with a non-whisper stub.
- Default wake word: `"hey jarvis"`. Default KWS model dir: `models/kws`.
- sherpa DLLs (`sherpa-onnx-c-api.dll`, `onnxruntime.dll`) ship next to `voice-agent.exe`.
- Every `go build`/`go test` that touches `fileindex` carries `-tags sqlite_fts5`; voice builds add `whisper`.
- Prefix go commands with `export PATH="$PATH:/c/w64devkit/bin"`.
- Commit trailers:
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`
  `Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr`
- Explicit `git add` only; never `git add -A`; never commit `config.json` or `*.onnx`.

## File Structure

- `internal/wakeword/loop.go` — **unchanged** (`FrameSource`, `Detector`, `runWakeLoop`).
- `internal/wakeword/engine.go` — **new, untagged**: `WakeEngine` interface + `kwsFrameLen` const. Referenced by `main.go` in both build configs.
- `internal/wakeword/keywords.go` — **new, untagged**: pure `selectKeyword` parser for the curated file. Unit-tested without CGO.
- `internal/wakeword/kws.go` — **new, `//go:build whisper`**: sherpa-backed `kwsEngine` (implements `WakeEngine`), `kwsDetector`, `NewKWS`.
- `internal/wakeword/kws_stub.go` — **new, `//go:build !whisper`**: `NewKWS` returns a not-available error.
- `internal/wakeword/porcupine.go`, `stub.go`, `hearer.go`, `hearer_stub.go` — **deleted**.
- `internal/audio/micsource.go` — **new**: malgo `MicSource` (a `FrameSource`) + testable `frameRing`.
- `internal/audio/micsource_test.go` — **new**: `frameRing` unit tests.
- `config/config.go` — add `WakeWord`, `WakeWordEnabled`, `KWSModelPath` + defaults; deprecate `PorcupineAccessKey`.
- `config/config_test.go` — extend for the new defaults.
- `cmd/app/main.go` — build the `WakeEngine`; wire wake loop + barge hearer; drop Porcupine.
- `go.mod` — add sherpa; remove `porcupine` + `pvrecorder` requires and the `pvrecorder` replace.
- `models/kws/keywords.txt` — **new, checked in** (curated, pre-tokenized). `*.onnx`/`tokens.txt` gitignored.
- `docs/BUILD-VOICE.md`, `.gitignore`, `CLAUDE.md` — model fetch + ignore + note.

---

### Task 0: Add sherpa dependency; verify combined build + onnxruntime reconciliation

**Files:**
- Create: `internal/wakeword/sherpa_dep.go` (`//go:build whisper`)
- Modify: `go.mod`, `go.sum` (via `go get`)

**Interfaces:**
- Produces: the `sherpa_onnx` import compiling under the `whisper` tag inside the real project (proves whisper-static + sherpa-dynamic coexist).

- [ ] **Step 1: Add the dependency**

Run:
```bash
export PATH="$PATH:/c/w64devkit/bin"
go get github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx@v1.13.7
```

- [ ] **Step 2: Add a build-tagged import anchor**

Create `internal/wakeword/sherpa_dep.go`:
```go
//go:build whisper

package wakeword

// Anchor the sherpa-onnx dependency in the whisper (voice) build. Task 4 replaces
// this file's role with the real engine; it exists first to prove the combined
// whisper.cpp(static) + sherpa-onnx(dynamic) build links in-project.
import _ "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
```

- [ ] **Step 3: Verify the combined build links**

Run:
```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -tags "whisper sqlite_fts5" -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
```
Expected: exit 0. If linking fails with duplicate/incompatible `onnxruntime` symbols, STOP — this is the Task 0 gate; resolve the onnxruntime version conflict (below) before continuing.

- [ ] **Step 4: Reconcile onnxruntime.dll**

The project already ships `./onnxruntime.dll` for BGE (loaded at runtime by `onnxruntime_go`). sherpa links its own. Copy sherpa's DLLs next to the exe and confirm the app still starts and the file index (BGE) still loads:
```bash
export PATH="$PATH:/c/w64devkit/bin"
GP=$(go env GOMODCACHE)
LIB="$GP/github.com/k2-fsa/sherpa-onnx-go-windows@v1.13.7/lib/x86_64-pc-windows-gnu"
cp "$LIB/sherpa-onnx-c-api.dll" "$LIB/sherpa-onnx-cxx-api.dll" .
# Compare onnxruntime versions; keep ONE. If sherpa's is newer and BGE still
# loads, replace ./onnxruntime.dll with sherpa's. Record the decision in the
# commit message.
cp "$LIB/onnxruntime.dll" ./onnxruntime.dll
```
Expected: `voice-agent.exe` launches; `voice-agent.log` shows the file index initializing without an onnxruntime load error. If BGE breaks, pin the older onnxruntime and note it.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/wakeword/sherpa_dep.go
git commit -m "$(cat <<'EOF'
build: add sherpa-onnx dep; verify combined whisper+sherpa build

Adds github.com/k2-fsa/sherpa-onnx-go v1.13.7 and an anchor import under the
whisper tag. Confirms whisper.cpp(static) + sherpa(dynamic DLLs) link in the
real project binary and that one onnxruntime.dll serves both sherpa and BGE.
[Record the onnxruntime version decision here.]

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr
EOF
)"
```

---

### Task 1: Config — wake-word fields + deprecate Porcupine

**Files:**
- Modify: `config/config.go` (struct ~line 10-22; consts ~line 88-99; `loadFromBytes` ~line 129-170)
- Test: `config/config_test.go`

**Interfaces:**
- Produces: `cfg.WakeWord string`, `cfg.WakeWordEnabled bool`, `cfg.KWSModelPath string`; consts `config.DefaultWakeWord = "hey jarvis"`, `config.DefaultKWSModelPath = "models/kws"`.

- [ ] **Step 1: Write the failing test**

Add to `config/config_test.go`:
```go
func TestWakeWordDefaults(t *testing.T) {
	// Absent keys: enabled true, default phrase + model dir.
	cfg, err := loadFromBytes([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.WakeWordEnabled {
		t.Error("WakeWordEnabled should default true when absent")
	}
	if cfg.WakeWord != DefaultWakeWord {
		t.Errorf("WakeWord = %q, want %q", cfg.WakeWord, DefaultWakeWord)
	}
	if cfg.KWSModelPath != DefaultKWSModelPath {
		t.Errorf("KWSModelPath = %q, want %q", cfg.KWSModelPath, DefaultKWSModelPath)
	}
	// Explicit false is honored.
	cfg2, _ := loadFromBytes([]byte(`{"wake_word_enabled": false}`))
	if cfg2.WakeWordEnabled {
		t.Error("explicit wake_word_enabled=false must be honored")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin"; go test -tags sqlite_fts5 ./config/ -run TestWakeWordDefaults`
Expected: FAIL — `WakeWord`/`WakeWordEnabled`/`KWSModelPath`/`DefaultWakeWord`/`DefaultKWSModelPath` undefined.

- [ ] **Step 3: Add fields, consts, and defaulting**

In `config/config.go`, add to the `Config` struct (near `PorcupineAccessKey`):
```go
	// Deprecated: Porcupine is removed; this field is ignored (kept for back-compat).
	PorcupineAccessKey string `json:"porcupine_access_key"`

	WakeWord        string `json:"wake_word"`         // curated phrase label; see models/kws/keywords.txt
	WakeWordEnabled bool   `json:"wake_word_enabled"` // defaults true when absent
	KWSModelPath    string `json:"kws_model_path"`    // dir holding the KWS model
```

Add consts near `DefaultBGEModelPath`:
```go
const (
	DefaultWakeWord     = "hey jarvis"
	DefaultKWSModelPath = "models/kws"
)
```

In `loadFromBytes`, inside the existing raw-map block, add:
```go
		if _, ok := raw["wake_word_enabled"]; !ok {
			cfg.WakeWordEnabled = true
		}
```
And after the BGE defaults:
```go
	if cfg.WakeWord == "" {
		cfg.WakeWord = DefaultWakeWord
	}
	if cfg.KWSModelPath == "" {
		cfg.KWSModelPath = DefaultKWSModelPath
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin"; go test -tags sqlite_fts5 ./config/ -run TestWakeWordDefaults`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "$(cat <<'EOF'
feat(config): wake_word / wake_word_enabled / kws_model_path; deprecate porcupine_access_key

Adds curated wake-word selection + KWS model dir with the same absent-vs-set
defaulting as trusted_execution/semantic_search (enabled defaults true).
porcupine_access_key is retained for back-compat but ignored.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr
EOF
)"
```

---

### Task 2: malgo `MicSource` (FrameSource) + testable ring

**Files:**
- Create: `internal/audio/micsource.go`
- Test: `internal/audio/micsource_test.go`

**Interfaces:**
- Consumes: the capture constants in `internal/audio/mic.go` (`sampleRate=16000`, `channels=1`).
- Produces: `audio.NewMicSource(frameLen int) *MicSource` with methods `Start() error`, `Stop() error`, `Read() ([]int16, error)` — satisfies `wakeword.FrameSource`.

- [ ] **Step 1: Write the failing test**

Create `internal/audio/micsource_test.go`:
```go
package audio

import "testing"

func TestFrameRingReadsFixedFramesFIFO(t *testing.T) {
	r := newFrameRing(4)
	r.push([]int16{1, 2, 3, 4, 5, 6}) // 6 samples buffered
	f1, err := r.read()
	if err != nil || len(f1) != 4 || f1[0] != 1 || f1[3] != 4 {
		t.Fatalf("frame1 = %v err=%v", f1, err)
	}
	r.push([]int16{7, 8}) // now 4 buffered: 5,6,7,8
	f2, _ := r.read()
	if f2[0] != 5 || f2[3] != 8 {
		t.Fatalf("frame2 = %v", f2)
	}
}

func TestFrameRingReadUnblocksOnClose(t *testing.T) {
	r := newFrameRing(4)
	done := make(chan error, 1)
	go func() { _, err := r.read(); done <- err }() // blocks: no data
	r.close()
	if err := <-done; err == nil {
		t.Fatal("read must return an error once the ring is closed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin"; go test ./internal/audio/ -run TestFrameRing`
Expected: FAIL — `newFrameRing` undefined.

- [ ] **Step 3: Implement the ring + the malgo source**

Create `internal/audio/micsource.go`:
```go
package audio

import (
	"encoding/binary"
	"errors"
	"math"
	"sync"

	"github.com/gen2brain/malgo"
)

// frameRing buffers int16 samples pushed from the capture callback and hands out
// fixed-size frames to a blocking Read. Split out from the device so its FIFO /
// close behavior is unit-testable without a microphone.
type frameRing struct {
	frameLen int
	mu       sync.Mutex
	cond     *sync.Cond
	buf      []int16
	closed   bool
}

func newFrameRing(frameLen int) *frameRing {
	r := &frameRing{frameLen: frameLen}
	r.cond = sync.NewCond(&r.mu)
	return r
}

func (r *frameRing) push(s []int16) {
	r.mu.Lock()
	r.buf = append(r.buf, s...)
	r.mu.Unlock()
	r.cond.Broadcast()
}

func (r *frameRing) read() ([]int16, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.buf) < r.frameLen && !r.closed {
		r.cond.Wait()
	}
	if r.closed {
		return nil, errors.New("mic source closed")
	}
	out := make([]int16, r.frameLen)
	copy(out, r.buf[:r.frameLen])
	r.buf = r.buf[r.frameLen:]
	return out, nil
}

func (r *frameRing) close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cond.Broadcast()
}

// MicSource is a malgo-backed wakeword.FrameSource: it captures 16 kHz mono audio
// and yields fixed-length int16 frames, replacing the Picovoice recorder so all
// capture runs on one runtime.
type MicSource struct {
	ring   *frameRing
	ctx    *malgo.AllocatedContext
	device *malgo.Device
}

func NewMicSource(frameLen int) *MicSource {
	return &MicSource{ring: newFrameRing(frameLen)}
}

func (m *MicSource) Start() error {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return err
	}
	m.ctx = ctx

	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatF32
	cfg.Capture.Channels = channels
	cfg.SampleRate = sampleRate
	cfg.Alsa.NoMMap = 1

	onRecv := func(_, in []byte, _ uint32) {
		n := len(in) / 4
		s := make([]int16, n)
		for i := 0; i < n; i++ {
			f := math.Float32frombits(binary.LittleEndian.Uint32(in[i*4:]))
			if f > 1 {
				f = 1
			} else if f < -1 {
				f = -1
			}
			s[i] = int16(f * 32767)
		}
		m.ring.push(s)
	}

	dev, err := malgo.InitDevice(ctx.Context, cfg, malgo.DeviceCallbacks{Data: onRecv})
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		m.ctx = nil
		return err
	}
	m.device = dev
	return dev.Start()
}

func (m *MicSource) Stop() error {
	if m.device != nil {
		_ = m.device.Stop()
		m.device.Uninit()
		m.device = nil
	}
	if m.ctx != nil {
		_ = m.ctx.Uninit()
		m.ctx.Free()
		m.ctx = nil
	}
	m.ring.close()
	return nil
}

func (m *MicSource) Read() ([]int16, error) { return m.ring.read() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin"; go test ./internal/audio/ -run TestFrameRing`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/audio/micsource.go internal/audio/micsource_test.go
git commit -m "$(cat <<'EOF'
feat(audio): malgo MicSource (FrameSource) to replace pvrecorder

A malgo-backed 16 kHz mono capture that yields fixed-size int16 frames via a
unit-tested frameRing, so the wake loop runs on the same runtime as command
capture and barge-in (Picovoice's recorder removed in the wiring task).

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr
EOF
)"
```

---

### Task 3: Curated keyword-file parsing + checked-in `keywords.txt`

**Files:**
- Create: `internal/wakeword/keywords.go` (untagged)
- Create: `internal/wakeword/keywords_test.go`
- Create: `models/kws/keywords.txt` (checked in)

**Interfaces:**
- Produces: `wakeword.selectKeyword(fileContent, wakeWord string) (line string, ok bool)` — returns the full keyword line whose `@label` matches `wakeWord` (case-insensitive, trimmed); `ok=false` if none.

- [ ] **Step 1: Write the failing test**

Create `internal/wakeword/keywords_test.go`:
```go
package wakeword

import "testing"

const sampleKeywords = "" +
	"▁HE ▁Y ▁JAR ▁VIS @hey jarvis\n" +
	"▁COM ▁PU ▁TER @computer\n" +
	"▁HE ▁Y ▁NO ▁VA @hey nova\n"

func TestSelectKeywordByLabel(t *testing.T) {
	line, ok := selectKeyword(sampleKeywords, "computer")
	if !ok || line != "▁COM ▁PU ▁TER @computer" {
		t.Fatalf("got %q ok=%v", line, ok)
	}
	// Case-insensitive + surrounding spaces on the requested label.
	if _, ok := selectKeyword(sampleKeywords, "  Hey Jarvis "); !ok {
		t.Error("label match should be case-insensitive and trimmed")
	}
	// Unknown label -> not found (caller falls back to the first line).
	if _, ok := selectKeyword(sampleKeywords, "banana"); ok {
		t.Error("unknown label must return ok=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin"; go test ./internal/wakeword/ -run TestSelectKeyword`
Expected: FAIL — `selectKeyword` undefined.

- [ ] **Step 3: Implement the parser**

Create `internal/wakeword/keywords.go`:
```go
package wakeword

import "strings"

// selectKeyword returns the full keyword line from a sherpa keywords file whose
// trailing "@label" matches wakeWord (case-insensitive, trimmed). The returned
// line is passed verbatim to NewKeywordStreamWithKeywords so only that phrase is
// armed. ok is false when no line matches; the caller then falls back to the
// first line.
func selectKeyword(fileContent, wakeWord string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(wakeWord))
	for _, raw := range strings.Split(fileContent, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if i := strings.Index(trimmed, "@"); i >= 0 {
			label := strings.ToLower(strings.TrimSpace(trimmed[i+1:]))
			if label == want {
				return trimmed, true
			}
		}
	}
	return "", false
}

// firstKeyword returns the first non-empty line, used as the fallback when the
// configured wake word is not present in the file.
func firstKeyword(fileContent string) (string, bool) {
	for _, raw := range strings.Split(fileContent, "\n") {
		if s := strings.TrimSpace(strings.TrimRight(raw, "\r")); s != "" {
			return s, true
		}
	}
	return "", false
}
```

- [ ] **Step 4: Create the checked-in curated file**

Create `models/kws/keywords.txt` with the four curated phrases. NOTE: the token
prefixes below are the standard sherpa gigaspeech BPE format; regenerate them
from the actual bundled model's `tokens.txt` using sherpa's
`text2token.py --tokens ... --tokens-type bpe --bpe-model ...` if the model
differs, keeping the `@label` suffix:
```
▁HE Y ▁JAR VIS @hey jarvis
▁COM PU TER @computer
▁HE Y ▁NO VA @hey nova
▁HE LLO ▁FRI DAY @hello friday
```

- [ ] **Step 5: Run test to verify it passes**

Run: `export PATH="$PATH:/c/w64devkit/bin"; go test ./internal/wakeword/ -run TestSelectKeyword`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/wakeword/keywords.go internal/wakeword/keywords_test.go models/kws/keywords.txt
git commit -m "$(cat <<'EOF'
feat(wakeword): curated keyword-file parser + checked-in keywords.txt

selectKeyword picks the armed phrase by its @label (case-insensitive), with a
firstKeyword fallback. keywords.txt ships the curated pick-list (Hey Jarvis /
Computer / Hey Nova / Hello Friday), pre-tokenized for the KWS model.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr
EOF
)"
```

---

### Task 4: sherpa-KWS engine (`WakeEngine` + `kwsEngine` + stub)

**Files:**
- Create: `internal/wakeword/engine.go` (untagged)
- Create: `internal/wakeword/kws.go` (`//go:build whisper`) — replaces `sherpa_dep.go`
- Create: `internal/wakeword/kws_stub.go` (`//go:build !whisper`)
- Delete: `internal/wakeword/sherpa_dep.go`
- Test: `internal/wakeword/engine_test.go`

**Interfaces:**
- Consumes: `selectKeyword`/`firstKeyword` (Task 3); `FrameSource`, `Detector`, `runWakeLoop` (loop.go); `audio.KeywordHearer` (internal/audio).
- Produces:
  - `type WakeEngine interface { Hearer() audio.KeywordHearer; StartLoop(ctx context.Context, src FrameSource, onDetect func(), isBusy func() bool) error; Close() }`
  - `const KWSFrameLen = 1600` (exported; `main.go` passes it to `audio.NewMicSource`)
  - `func NewKWS(modelDir, wakeWord string) (WakeEngine, error)` (per-tag).

- [ ] **Step 1: Write the failing test (interface + stub contract, no model needed)**

Create `internal/wakeword/engine_test.go`:
```go
package wakeword

import (
	"context"
	"testing"
	"time"
)

// NewKWS must fail cleanly when the model dir is absent (both build tags): the
// caller degrades to "wake word off", never a crash.
func TestNewKWSMissingModelFailsCleanly(t *testing.T) {
	eng, err := NewKWS("does/not/exist", "hey jarvis")
	if err == nil {
		if eng != nil {
			eng.Close()
		}
		t.Fatal("expected an error when the KWS model dir is missing")
	}
	if eng != nil {
		t.Fatal("engine must be nil on error")
	}
}

// The FrameSource/Detector seam still drives runWakeLoop (guards the abstraction
// the engine plugs into). Uses fakes, no CGO.
func TestRunWakeLoopFiresOnDetect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fired := make(chan struct{}, 1)
	src := &fakeSource{frame: []int16{0}}
	det := &fakeDetector{hitAfter: 3}
	go runWakeLoop(ctx, src, det, func() { fired <- struct{}{}; cancel() }, nil)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("onDetect never fired")
	}
}
```
If `fakeSource`/`fakeDetector` already exist in `loop_test.go`, reuse them and drop the redefinition; otherwise add minimal ones:
```go
type fakeSource struct{ frame []int16 }

func (f *fakeSource) Read() ([]int16, error) { return f.frame, nil }
func (f *fakeSource) Start() error           { return nil }
func (f *fakeSource) Stop() error            { return nil }

type fakeDetector struct{ n, hitAfter int }

func (d *fakeDetector) Process([]int16) (int, error) {
	d.n++
	if d.n >= d.hitAfter {
		return 0, nil
	}
	return -1, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin"; go test -tags "whisper sqlite_fts5" ./internal/wakeword/ -run 'TestNewKWS|TestRunWakeLoop'`
Expected: FAIL — `NewKWS`/`WakeEngine` undefined.

- [ ] **Step 3: Add the untagged interface**

Create `internal/wakeword/engine.go`:
```go
package wakeword

import (
	"context"

	"github.com/yourname/voice-agent/internal/audio"
)

// KWSFrameLen is the capture chunk (samples @16 kHz) fed to KWS per Read. The
// sherpa transducer accepts arbitrary chunk sizes; 1600 = 100 ms keeps latency
// low without spinning. Exported because main.go passes it to audio.NewMicSource.
const KWSFrameLen = 1600

// WakeEngine is the tag-independent handle main.go holds. The whisper build backs
// it with sherpa KWS; the non-whisper build has no implementation (NewKWS errors).
type WakeEngine interface {
	// Hearer returns a barge-in keyword detector sharing this engine's model.
	Hearer() audio.KeywordHearer
	// StartLoop runs the idle wake loop on src until ctx is cancelled, calling
	// onDetect (which must BLOCK until the command completes) on a wake-word hit.
	StartLoop(ctx context.Context, src FrameSource, onDetect func(), isBusy func() bool) error
	// Close releases the underlying model.
	Close()
}
```

- [ ] **Step 4: Add the whisper implementation** (delete `sherpa_dep.go` first)

Create `internal/wakeword/kws.go`:
```go
//go:build whisper

package wakeword

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
```

- [ ] **Step 5: Add the non-whisper stub**

Create `internal/wakeword/kws_stub.go`:
```go
//go:build !whisper

package wakeword

import "errors"

// NewKWS is unavailable without the whisper (voice) build tag.
func NewKWS(modelDir, wakeWord string) (WakeEngine, error) {
	return nil, errors.New("wake word requires the whisper build")
}
```

Delete the anchor: `git rm internal/wakeword/sherpa_dep.go`.

- [ ] **Step 6: Run tests (both tags) to verify they pass**

Run:
```bash
export PATH="$PATH:/c/w64devkit/bin"
go test ./internal/wakeword/ -run 'TestNewKWS|TestRunWakeLoop|TestSelectKeyword'          # non-whisper
go test -tags "whisper sqlite_fts5" ./internal/wakeword/ -run 'TestNewKWS|TestRunWakeLoop' # whisper
```
Expected: PASS in both. (`TestNewKWSMissingModel...` passes both ways: stub errors; whisper errors on the missing dir.)

- [ ] **Step 7: Commit**

```bash
git add internal/wakeword/engine.go internal/wakeword/kws.go internal/wakeword/kws_stub.go internal/wakeword/engine_test.go
git rm internal/wakeword/sherpa_dep.go
git commit -m "$(cat <<'EOF'
feat(wakeword): sherpa-onnx KWS engine behind a tag-independent WakeEngine

NewKWS loads the transducer + tokens + keywords, arms the selected phrase, and
exposes a Detector (idle loop) and a KeywordHearer (barge-in) over one shared
spotter. Missing model files -> clean error so the caller disables the wake word.
Non-whisper stub returns "requires the whisper build".

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr
EOF
)"
```

---

### Task 5: Wire main.go; remove Porcupine + pvrecorder

**Files:**
- Modify: `cmd/app/main.go` (barge wiring ~line 236-258; wake loop ~line 480-495)
- Delete: `internal/wakeword/porcupine.go`, `internal/wakeword/stub.go`, `internal/wakeword/hearer.go`, `internal/wakeword/hearer_stub.go`
- Modify: `go.mod` (drop porcupine + pvrecorder requires + the pvrecorder replace)

**Interfaces:**
- Consumes: `wakeword.NewKWS`, `wakeword.WakeEngine`, `wakeword.KWSFrameLen` (from Task 4), `audio.NewMicSource`, `audio.WatchForBargeIn`, `audio.KeywordHearer`.

- [ ] **Step 1: Replace the barge-in hearer wiring in main.go**

Find the block added in the barge-in commit (uses `wakeword.NewKeywordHearer`) and replace it so the engine provides the hearer. Change:
```go
	if cfg.EnableVoice {
		var kwHearer audio.KeywordHearer
		if cfg.PorcupineAccessKey != "" {
			if h, closeH, err := wakeword.NewKeywordHearer(cfg.PorcupineAccessKey); err == nil && h != nil {
				kwHearer = h
				defer closeH()
				...
			}
		}
		executor.BargeWatch = func(ctx context.Context, stop func()) {
			audio.WatchForBargeIn(ctx, kwHearer, stop)
		}
		audio.SetLevelSink(ui.PushMicLevel)
	}
```
to:
```go
	// Build the local KWS wake engine once (keyless). It powers BOTH the idle
	// wake loop and the barge-in interrupt. Missing model -> nil -> wake word off,
	// open-mic barge-in still works.
	var wake wakeword.WakeEngine
	if cfg.EnableVoice && cfg.WakeWordEnabled {
		if w, err := wakeword.NewKWS(cfg.KWSModelPath, cfg.WakeWord); err == nil {
			wake = w
			defer wake.Close()
			log.Printf("[wake] KWS active — say %q", cfg.WakeWord)
		} else {
			log.Printf("[wake] KWS unavailable: %v (wake word off; pill + typing still work)", err)
		}
	}
	if cfg.PorcupineAccessKey != "" {
		log.Println("[config] porcupine_access_key is deprecated and ignored (KWS is local + keyless)")
	}

	if cfg.EnableVoice {
		var kwHearer audio.KeywordHearer
		if wake != nil {
			kwHearer = wake.Hearer()
		}
		executor.BargeWatch = func(ctx context.Context, stop func()) {
			audio.WatchForBargeIn(ctx, kwHearer, stop)
		}
		audio.SetLevelSink(ui.PushMicLevel)
	}
```

- [ ] **Step 2: Replace the wake-word loop wiring in main.go**

Change the Porcupine loop:
```go
	if cfg.EnableVoice && cfg.PorcupineAccessKey != "" {
		go func() {
			onDetect := func() { engineApp.TriggerAndWait(60 * time.Second) }
			busy := func() bool { return engineApp.IsBusy() || executor.IsSpeaking() }
			if err := wakeword.StartWakeWordLoop(rootCtx, cfg.PorcupineAccessKey, onDetect, busy); err != nil {
				log.Printf("wake word stopped: %v", err)
			}
		}()
	}
```
to:
```go
	if wake != nil {
		go func() {
			src := audio.NewMicSource(wakeword.KWSFrameLen)
			onDetect := func() { engineApp.TriggerAndWait(60 * time.Second) }
			busy := func() bool { return engineApp.IsBusy() || executor.IsSpeaking() }
			if err := wake.StartLoop(rootCtx, src, onDetect, busy); err != nil {
				log.Printf("wake word stopped: %v", err)
			}
		}()
	}
```

- [ ] **Step 3: Delete the Porcupine files and drop the deps**

```bash
git rm internal/wakeword/porcupine.go internal/wakeword/stub.go internal/wakeword/hearer.go internal/wakeword/hearer_stub.go
export PATH="$PATH:/c/w64devkit/bin"
go mod edit -droprequire github.com/Picovoice/porcupine/binding/go/v3
go mod edit -droprequire github.com/Picovoice/pvrecorder/binding/go
go mod edit -dropreplace github.com/Picovoice/pvrecorder/binding/go
go mod tidy
```

- [ ] **Step 4: Build both configs + full test suite**

Run:
```bash
export PATH="$PATH:/c/w64devkit/bin"
go build -tags sqlite_fts5 ./...
go build -tags "whisper sqlite_fts5" -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
go test -tags sqlite_fts5 ./...
```
Expected: all succeed. (`go build -tags sqlite_fts5 ./...` proves the non-whisper stub path; the whisper exe proves the real link; the suite is green.)

- [ ] **Step 5: Commit**

```bash
git add cmd/app/main.go go.mod go.sum
git rm internal/wakeword/porcupine.go internal/wakeword/stub.go internal/wakeword/hearer.go internal/wakeword/hearer_stub.go
git commit -m "$(cat <<'EOF'
feat(wakeword): wire sherpa KWS; remove Porcupine + pvrecorder

main builds one keyless KWS engine that drives the idle wake loop (on a malgo
MicSource) and the barge-in hearer. Deletes the Porcupine detector, the old
StartWakeWordLoop stub, and the Porcupine-based barge hearer; drops the
porcupine + pvrecorder modules and the pvrecorder replace. porcupine_access_key
is ignored with a one-line notice. Picovoice is gone.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr
EOF
)"
```

---

### Task 6: Docs, gitignore, and CLAUDE.md

**Files:**
- Modify: `docs/BUILD-VOICE.md`, `.gitignore`, `CLAUDE.md`

**Interfaces:** none (documentation).

- [ ] **Step 1: Ignore the KWS model binaries**

Append to `.gitignore`:
```
# KWS wake-word model (fetched locally; keywords.txt IS tracked)
models/kws/*.onnx
models/kws/tokens.txt
```

- [ ] **Step 2: Document the model fetch in docs/BUILD-VOICE.md**

Add a "Wake word (KWS) model" section: download a sherpa-onnx streaming KWS
transducer (e.g. `sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01`), place
`encoder.onnx`, `decoder.onnx`, `joiner.onnx`, `tokens.txt` under `models/kws/`
(the curated `keywords.txt` is already checked in), and copy
`sherpa-onnx-c-api.dll` + `onnxruntime.dll` (from the sherpa-onnx-go-windows
module's `lib/x86_64-pc-windows-gnu`) next to `voice-agent.exe`. Note that if the
downloaded model's tokenizer differs, regenerate `keywords.txt` with sherpa's
`text2token.py` (keep the `@label` suffixes).

- [ ] **Step 3: Update CLAUDE.md**

In the Key Dependencies / config sections, replace the Porcupine wake-word line
with sherpa-onnx KWS, and update the config field list: remove
`porcupine_access_key` (note it's deprecated/ignored), add `wake_word`,
`wake_word_enabled`, `kws_model_path`.

- [ ] **Step 4: Commit**

```bash
git add docs/BUILD-VOICE.md .gitignore CLAUDE.md
git commit -m "$(cat <<'EOF'
docs: KWS wake-word model fetch + config; ignore model binaries

BUILD-VOICE.md gains the sherpa KWS model + DLL steps; CLAUDE.md swaps Porcupine
for sherpa-onnx KWS and updates the config field list; .gitignore excludes the
KWS .onnx/tokens (keywords.txt stays tracked).

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01QNe8i5nG12WvyoVqpS4qZr
EOF
)"
```

---

## Verification (whole feature)

- [ ] `go build -tags sqlite_fts5 ./...` (non-whisper) — clean.
- [ ] `go build -tags "whisper sqlite_fts5" -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app` — links.
- [ ] `go test -tags sqlite_fts5 ./...` — green.
- [ ] With `models/kws/` populated + DLLs beside the exe: launch, say the configured wake word → island activates; say the wake word while it speaks → barge-in stops it. Remove `models/kws/` → wake word disabled, pill + typing + open-mic barge-in still work, no crash.
- [ ] `git grep -i porcupine -- ':!docs' ':!*.md'` returns nothing in code (only historical doc/spec mentions remain).
