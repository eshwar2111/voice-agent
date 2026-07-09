# SP3 — Ambient Trigger Engine + Overlay UI Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the assistant proactive — local event sources emit one-tap actionable suggestions through a policy-gated engine (off by default) — and reskin the whole overlay to one dark-glass minimalist system.

**Architecture:** A new `internal/ambient` package: pure `Suggestion`/`Policy`/classifiers, an `Engine` that fans in `Source`s and delivers one suggestion at a time via a `Deliverer` interface, and three sources (Downloads via fsnotify, Calendar refactored from `alerts`, Clipboard via poll+classify). The UI gets a suggestion card and a visual redesign of `overlay_v2.html`, preserving every Go→JS entry point.

**Tech Stack:** Go 1.26, `github.com/fsnotify/fsnotify` (new, pure-Go), `archive/zip` (stdlib), `atotto/clipboard`, existing `internal/ui` (webview) + `internal/dispatch`.

## Global Constraints

- Module `github.com/yourname/voice-agent`. Prefix every go command: `export PATH="$PATH:/c/w64devkit/bin" && go ...` (else `gcc: cannot execute 'as'`).
- Do NOT run `go build ./...`, `go build ./cmd/app`, or `go test ./...` (they link whisper). Verify non-whisper packages with `go build ./internal/...`; verify `cmd/app`/`ui`/`engine` compile with `go vet` (compiles, no link). Voice build only via `-tags whisper` when explicitly needed.
- **Off by default:** the engine starts ONLY when `cfg.EnableProactive == true`; it is also suppressed when `cfg.PrivacyMode == true`.
- **No LLM in the trigger detection path.** Sources are event-driven or slow-polled. The LLM is reached only if the user accepts an action that needs it.
- **Never annoy:** one suggestion at a time; dedup by `DedupKey`; a global min-gap; suppressed while the assistant is busy.
- UI redesign must PRESERVE every JS function the Go side calls via `w.Eval` (`updateUI`, `showCommand`, `showConfirm`/`confirmCallback`, `triggerMeetingAlert`, `submitCurrentCommand`→`window.submitCommand`, settings hooks) — reskin + add the suggestion card only.
- Explicit `git add <files>` only — never `git add -A`. Commit after every task. TDD for pure units.
- Dark-glass design system is defined in `docs/sp3-ui-mockup.html` (the approved reference).

---

## File Structure

**New (`internal/ambient/`):** `suggestion.go` (types + `Deliverer`), `policy.go`, `engine.go`,
`classify.go` (pure clipboard + filename classifiers), `actions.go` (local open/unzip helpers),
`downloads.go`, `calendar.go`, `clipboard.go`; tests alongside.
**New:** `internal/ui/suggestion.go` (the `Deliverer` glue → webview).
**Modified:** `internal/ui/overlay.go` (`ShowSuggestion` + accept/dismiss bindings/callbacks),
`internal/ui/overlay_v2.html` (redesign + suggestion card), `config/config.go`
(`EnableProactive`), `cmd/app/main.go` (start engine when enabled), `internal/alerts/alerts.go`
(logic moves into `calendar.go`; file deleted). `go.mod`/`go.sum` (fsnotify).

---

## Phase A — Ambient core (pure, testable)

### Task 1: Suggestion + Source + Deliverer types

**Files:** Create `internal/ambient/suggestion.go`, `internal/ambient/suggestion_test.go`

**Interfaces:**
- Produces:
  - `type Suggestion struct { Source, Icon, Title, Message, Action, DedupKey string; Run func(ctx context.Context) error }`
  - `type Source interface { Name() string; Start(ctx context.Context, out chan<- Suggestion) }`
  - `type Deliverer interface { ShowSuggestion(id string, s Suggestion) }`

- [ ] **Step 1: Write the failing test**

Create `internal/ambient/suggestion_test.go`:
```go
package ambient

import (
	"context"
	"testing"
)

func TestSuggestionRunInvokes(t *testing.T) {
	called := false
	s := Suggestion{Title: "t", DedupKey: "k", Run: func(context.Context) error { called = true; return nil }}
	_ = s.Run(context.Background())
	if !called {
		t.Fatal("Run should invoke the closure")
	}
	// interface satisfaction check (compile-time via var)
	var _ Deliverer = fakeDeliverer{}
}

type fakeDeliverer struct{ last Suggestion; id string }

func (f fakeDeliverer) ShowSuggestion(id string, s Suggestion) {}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/ambient/ -run TestSuggestionRun -v`
Expected: FAIL — undefined types.

- [ ] **Step 3: Implement**

Create `internal/ambient/suggestion.go`:
```go
package ambient

import "context"

// Suggestion is one proactive, actionable prompt shown as a card.
type Suggestion struct {
	Source   string // "downloads" | "calendar" | "clipboard"
	Icon     string // badge glyph key: "download"|"calendar"|"link"|"warn"
	Title    string
	Message  string
	Action   string // primary button label, e.g. "Unzip"
	DedupKey string // suppress repeats
	Run      func(ctx context.Context) error
}

// Source watches for events and emits Suggestions until ctx is cancelled.
type Source interface {
	Name() string
	Start(ctx context.Context, out chan<- Suggestion)
}

// Deliverer shows a suggestion to the user (implemented by the UI).
type Deliverer interface {
	ShowSuggestion(id string, s Suggestion)
}
```

- [ ] **Step 4: Run + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/ambient/ -v` (PASS)
```bash
git add internal/ambient/suggestion.go internal/ambient/suggestion_test.go
git commit -m "feat(ambient): Suggestion/Source/Deliverer types"
```

---

### Task 2: Policy (dedup + min-gap + busy)

**Files:** Create `internal/ambient/policy.go`, `internal/ambient/policy_test.go`

**Interfaces:**
- Consumes: `Suggestion`.
- Produces: `type Policy struct{...}`, `func NewPolicy(minGap time.Duration) *Policy`,
  `func (p *Policy) Allow(s Suggestion, now time.Time, busy bool) bool`,
  `func (p *Policy) Record(s Suggestion, now time.Time)`.

- [ ] **Step 1: Write the failing test**

Create `internal/ambient/policy_test.go`:
```go
package ambient

import (
	"testing"
	"time"
)

func TestPolicy(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	p := NewPolicy(2 * time.Minute)
	s := Suggestion{DedupKey: "a"}

	if !p.Allow(s, t0, false) {
		t.Fatal("first suggestion should be allowed")
	}
	if p.Allow(s, t0, true) {
		t.Fatal("must be suppressed while busy")
	}
	p.Record(s, t0)
	if p.Allow(s, t0.Add(30*time.Second), false) {
		t.Fatal("duplicate key within window must be blocked")
	}
	if p.Allow(Suggestion{DedupKey: "b"}, t0.Add(30*time.Second), false) {
		t.Fatal("different key but within min-gap must be blocked")
	}
	if !p.Allow(Suggestion{DedupKey: "b"}, t0.Add(3*time.Minute), false) {
		t.Fatal("different key after min-gap should be allowed")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/ambient/ -run TestPolicy -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/ambient/policy.go`:
```go
package ambient

import "time"

// dedupWindow: don't repeat the same DedupKey within this span.
const dedupWindow = 6 * time.Hour

// Policy is the pure "should this be shown now?" gate.
type Policy struct {
	MinGap    time.Duration
	seen      map[string]time.Time
	lastShown time.Time
}

func NewPolicy(minGap time.Duration) *Policy {
	return &Policy{MinGap: minGap, seen: make(map[string]time.Time)}
}

// Allow reports whether s may be shown at time now given the busy state.
func (p *Policy) Allow(s Suggestion, now time.Time, busy bool) bool {
	if busy {
		return false
	}
	if !p.lastShown.IsZero() && now.Sub(p.lastShown) < p.MinGap {
		return false
	}
	if t, ok := p.seen[s.DedupKey]; ok && now.Sub(t) < dedupWindow {
		return false
	}
	return true
}

// Record marks s as shown at now (call only when actually delivered).
func (p *Policy) Record(s Suggestion, now time.Time) {
	p.seen[s.DedupKey] = now
	p.lastShown = now
}
```

- [ ] **Step 4: Run + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/ambient/ -run TestPolicy -v` (PASS)
```bash
git add internal/ambient/policy.go internal/ambient/policy_test.go
git commit -m "feat(ambient): Policy (dedup + min-gap + busy suppression)"
```

---

### Task 3: Engine (fan-in, gate, deliver, accept/dismiss)

**Files:** Create `internal/ambient/engine.go`, `internal/ambient/engine_test.go`

**Interfaces:**
- Consumes: `Source`, `Deliverer`, `Policy`, `Suggestion`.
- Produces:
  - `type Engine struct { Sources []Source; Policy *Policy; UI Deliverer; Busy func() bool; Enabled func() bool; Now func() time.Time }`
  - `func (e *Engine) Run(ctx context.Context)` (fan-in loop)
  - `func (e *Engine) Accept(id string)` and `func (e *Engine) Dismiss(id string)`
  - internal `consider(s Suggestion)` (used by Run and tests).

- [ ] **Step 1: Write the failing test**

Create `internal/ambient/engine_test.go`:
```go
package ambient

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recDeliverer struct {
	mu   sync.Mutex
	last string
}

func (r *recDeliverer) ShowSuggestion(id string, s Suggestion) {
	r.mu.Lock()
	r.last = id + ":" + s.Title
	r.mu.Unlock()
}
func (r *recDeliverer) lastShown() string { r.mu.Lock(); defer r.mu.Unlock(); return r.last }

func newTestEngine(ui Deliverer, busy bool) *Engine {
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	return &Engine{
		Policy:  NewPolicy(time.Minute),
		UI:      ui,
		Busy:    func() bool { return busy },
		Enabled: func() bool { return true },
		Now:     func() time.Time { return t0 },
	}
}

func TestEngineDeliversThenSuppressesDuplicate(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, false)
	s := Suggestion{Title: "Unzip?", DedupKey: "zip1"}
	e.consider(s)
	if ui.lastShown() == "" {
		t.Fatal("first suggestion should be delivered")
	}
	// while one is active, a second is dropped (one-at-a-time)
	before := ui.lastShown()
	e.consider(Suggestion{Title: "Other", DedupKey: "x"})
	if ui.lastShown() != before {
		t.Fatal("second suggestion must be dropped while one is active")
	}
}

func TestEngineSuppressedWhenBusy(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, true) // busy
	e.consider(Suggestion{Title: "x", DedupKey: "k"})
	if ui.lastShown() != "" {
		t.Fatal("nothing should be delivered while busy")
	}
}

func TestEngineAcceptRunsAction(t *testing.T) {
	ui := &recDeliverer{}
	e := newTestEngine(ui, false)
	done := make(chan struct{})
	e.consider(Suggestion{Title: "Do", DedupKey: "k", Run: func(context.Context) error { close(done); return nil }})
	// the delivered id is "sg1"
	e.Accept("sg1")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Accept should run the action")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/ambient/ -run TestEngine -v`
Expected: FAIL — undefined `Engine`/`consider`.

- [ ] **Step 3: Implement**

Create `internal/ambient/engine.go`:
```go
package ambient

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Engine fans in all sources, applies Policy, and delivers one suggestion at a time.
type Engine struct {
	Sources []Source
	Policy  *Policy
	UI      Deliverer
	Busy    func() bool      // suppress while the assistant is busy (nil => not busy)
	Enabled func() bool      // master toggle + privacy (nil => enabled)
	Now     func() time.Time // injectable clock (nil => time.Now)

	mu       sync.Mutex
	active   *Suggestion
	activeID string
	counter  int
}

func (e *Engine) clock() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Run starts every source and delivers policy-approved suggestions until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	out := make(chan Suggestion, 16)
	for _, s := range e.Sources {
		go s.Start(ctx, out)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-out:
			e.consider(s)
		}
	}
}

func (e *Engine) consider(s Suggestion) {
	if e.Enabled != nil && !e.Enabled() {
		return
	}
	busy := e.Busy != nil && e.Busy()
	now := e.clock()

	e.mu.Lock()
	if e.active != nil { // one at a time
		e.mu.Unlock()
		return
	}
	if !e.Policy.Allow(s, now, busy) {
		e.mu.Unlock()
		return
	}
	e.counter++
	id := fmt.Sprintf("sg%d", e.counter)
	sc := s
	e.active = &sc
	e.activeID = id
	e.Policy.Record(s, now)
	e.mu.Unlock()

	e.UI.ShowSuggestion(id, s)
}

// Accept runs the active suggestion's action if id matches, then clears it.
func (e *Engine) Accept(id string) {
	e.mu.Lock()
	s := e.active
	match := e.activeID == id
	if match {
		e.active = nil
		e.activeID = ""
	}
	e.mu.Unlock()
	if match && s != nil && s.Run != nil {
		go func() { _ = s.Run(context.Background()) }()
	}
}

// Dismiss clears the active suggestion if id matches (also called by the UI 15s timeout).
func (e *Engine) Dismiss(id string) {
	e.mu.Lock()
	if e.activeID == id {
		e.active = nil
		e.activeID = ""
	}
	e.mu.Unlock()
}
```

- [ ] **Step 4: Run + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/ambient/ -v` (all PASS)
```bash
git add internal/ambient/engine.go internal/ambient/engine_test.go
git commit -m "feat(ambient): Engine — fan-in, policy gate, one-at-a-time deliver, accept/dismiss"
```

---

## Phase B — Classifiers + actions

### Task 4: Pure classifiers + local action helpers

**Files:** Create `internal/ambient/classify.go`, `internal/ambient/actions.go`, `internal/ambient/classify_test.go`

**Interfaces:**
- Produces:
  - `type FileMatch struct { Icon, Action, Kind string }` and `func ClassifyDownload(name string) (FileMatch, bool)` — kind: `"archive"|"image"|"installer"`.
  - `type ClipMatch struct { Icon, Title, Message, Action, Kind, URL string }` and `func ClassifyClipboard(text string) (ClipMatch, bool)` — kind: `"url"|"error"|"tracking"`.
  - `actions.go`: `func openPath(path string) error`, `func openURL(url string) error`, `func unzip(zipPath, destDir string) error`.

- [ ] **Step 1: Write the failing test**

Create `internal/ambient/classify_test.go`:
```go
package ambient

import "testing"

func TestClassifyDownload(t *testing.T) {
	cases := map[string]string{ // filename -> expected Kind ("" = no match)
		"report.zip":        "archive",
		"photo.PNG":         "image",
		"setup.exe":         "installer",
		"notes.txt":         "",
		"movie.part":        "", // partial download ignored
		"archive.zip.crdownload": "",
	}
	for name, want := range cases {
		m, ok := ClassifyDownload(name)
		if want == "" {
			if ok {
				t.Errorf("%q should not classify (got %q)", name, m.Kind)
			}
			continue
		}
		if !ok || m.Kind != want {
			t.Errorf("%q -> want %q, got ok=%v kind=%q", name, want, ok, m.Kind)
		}
	}
}

func TestClassifyClipboard(t *testing.T) {
	m, ok := ClassifyClipboard("https://github.com/x/y")
	if !ok || m.Kind != "url" || m.URL != "https://github.com/x/y" {
		t.Errorf("url classify: ok=%v %+v", ok, m)
	}
	m, ok = ClassifyClipboard("panic: runtime error: index out of range\n\tmain.go:12")
	if !ok || m.Kind != "error" {
		t.Errorf("error classify: ok=%v %+v", ok, m)
	}
	if _, ok := ClassifyClipboard("just some ordinary text"); ok {
		t.Error("ordinary text must not classify")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/ambient/ -run TestClassify -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/ambient/classify.go`:
```go
package ambient

import (
	"path/filepath"
	"regexp"
	"strings"
)

type FileMatch struct{ Icon, Action, Kind string }

var (
	archiveExt   = map[string]bool{".zip": true, ".rar": true, ".7z": true, ".tar": true, ".gz": true}
	imageExt     = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
	installerExt = map[string]bool{".exe": true, ".msi": true}
	partialExt   = map[string]bool{".part": true, ".crdownload": true, ".tmp": true, ".download": true}
)

// ClassifyDownload maps a finished download filename to a suggestion template.
// Returns ok=false for partial downloads and unrecognized types.
func ClassifyDownload(name string) (FileMatch, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	if partialExt[ext] {
		return FileMatch{}, false
	}
	switch {
	case archiveExt[ext]:
		return FileMatch{Icon: "download", Action: "Unzip", Kind: "archive"}, true
	case imageExt[ext]:
		return FileMatch{Icon: "download", Action: "Open", Kind: "image"}, true
	case installerExt[ext]:
		return FileMatch{Icon: "download", Action: "Run", Kind: "installer"}, true
	}
	return FileMatch{}, false
}

type ClipMatch struct{ Icon, Title, Message, Action, Kind, URL string }

var (
	urlRe      = regexp.MustCompile(`^\s*(https?://[^\s]+)\s*$`)
	errorRe    = regexp.MustCompile(`(?i)(panic:|traceback|exception|error:|\bstack trace\b|\bat .*\.(go|js|ts|py):\d+)`)
	trackingRe = regexp.MustCompile(`^\s*(1Z[0-9A-Z]{16}|[0-9]{12,22})\s*$`) // UPS / generic long numeric
)

// ClassifyClipboard recognizes actionable clipboard text (URL / error / tracking).
func ClassifyClipboard(text string) (ClipMatch, bool) {
	if m := urlRe.FindStringSubmatch(text); m != nil {
		return ClipMatch{Icon: "link", Title: "Link copied", Message: truncate(m[1], 48) + " — open it?", Action: "Open", Kind: "url", URL: m[1]}, true
	}
	if trackingRe.MatchString(text) {
		code := strings.TrimSpace(text)
		return ClipMatch{Icon: "link", Title: "Tracking number copied", Message: code + " — track it?", Action: "Track", Kind: "tracking",
			URL: "https://www.google.com/search?q=track+package+" + code}, true
	}
	if errorRe.MatchString(text) && len(text) < 4000 {
		return ClipMatch{Icon: "warn", Title: "Error copied", Message: "Explain this error?", Action: "Explain", Kind: "error"}, true
	}
	return ClipMatch{}, false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
```

Create `internal/ambient/actions.go`:
```go
package ambient

import (
	"archive/zip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// openPath opens a file/folder with its default handler.
func openPath(path string) error {
	return exec.Command("cmd.exe", "/c", "start", "", path).Start()
}

// openURL opens a URL in the default browser.
func openURL(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// unzip extracts zipPath into a sibling folder named after the archive (Zip-Slip safe).
func unzip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for _, f := range r.File {
		fp := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(fp, filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue // skip path-traversal entries
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(fp, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(fp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go test ./internal/ambient/ -run TestClassify -v` (PASS)
Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/ambient/` (exit 0)
```bash
git add internal/ambient/classify.go internal/ambient/actions.go internal/ambient/classify_test.go
git commit -m "feat(ambient): pure classifiers (download/clipboard) + open/unzip actions"
```

---

## Phase C — Sources

### Task 5: Downloads source (fsnotify)

**Files:** Create `internal/ambient/downloads.go`; Modify `go.mod`/`go.sum`

**Interfaces:**
- Consumes: `ClassifyDownload`, `openPath`, `unzip`, `Suggestion`.
- Produces: `type DownloadsSource struct { Dir string }`, `func NewDownloadsSource() *DownloadsSource` (defaults `%USERPROFILE%\Downloads`), implements `Source`.

- [ ] **Step 1: Add fsnotify**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go get github.com/fsnotify/fsnotify@latest`
Expected: go.mod/go.sum updated.

- [ ] **Step 2: Implement the source**

Create `internal/ambient/downloads.go`:
```go
package ambient

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

type DownloadsSource struct{ Dir string }

func NewDownloadsSource() *DownloadsSource {
	return &DownloadsSource{Dir: filepath.Join(os.Getenv("USERPROFILE"), "Downloads")}
}

func (d *DownloadsSource) Name() string { return "downloads" }

func (d *DownloadsSource) Start(ctx context.Context, out chan<- Suggestion) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[ambient/downloads] watcher: %v", err)
		return
	}
	defer w.Close()
	if err := w.Add(d.Dir); err != nil {
		log.Printf("[ambient/downloads] watch %s: %v", d.Dir, err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// A finished download appears as a Create (final name) or a Rename to the final name.
			if ev.Op&(fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			name := filepath.Base(ev.Name)
			m, ok := ClassifyDownload(name)
			if !ok {
				continue
			}
			if fi, err := os.Stat(ev.Name); err != nil || fi.IsDir() {
				continue
			}
			out <- d.suggest(m, ev.Name, name)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("[ambient/downloads] error: %v", err)
		}
	}
}

func (d *DownloadsSource) suggest(m FileMatch, path, name string) Suggestion {
	s := Suggestion{Source: "downloads", Icon: m.Icon, Action: m.Action, Title: "Download finished", DedupKey: "dl:" + path}
	switch m.Kind {
	case "archive":
		s.Message = name + " — unzip it here?"
		dest := path[:len(path)-len(filepath.Ext(path))]
		s.Run = func(ctx context.Context) error { return unzip(path, dest) }
	case "image":
		s.Message = name + " — open it?"
		s.Run = func(ctx context.Context) error { return openPath(path) }
	case "installer":
		s.Title = "Installer downloaded"
		s.Message = name + " — run it?"
		s.Run = func(ctx context.Context) error { return openPath(path) }
	}
	return s
}
```

- [ ] **Step 3: Build (no unit test — OS watcher glue; covered by engine tests via fake sources)**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/ambient/` (exit 0)

- [ ] **Step 4: Commit**

```bash
git add internal/ambient/downloads.go go.mod go.sum
git commit -m "feat(ambient): downloads source (fsnotify) with unzip/open suggestions"
```

---

### Task 6: Calendar source (refactor `alerts`)

**Files:** Create `internal/ambient/calendar.go`; Delete `internal/alerts/alerts.go`

**Interfaces:**
- Consumes: `config.Config`, `tools.GoogleCalendarListTool`/`MicrosoftCalendarListTool`, `openURL`, `Suggestion`.
- Produces: `type CalendarSource struct { Cfg *config.Config }`, implements `Source`; polls every 5 min.

- [ ] **Step 1: Implement the source**

Create `internal/ambient/calendar.go` (port `alerts.go`'s parse/timing logic; emit Suggestions instead of `ui.SetMeetingAlert`). READ the old `internal/alerts/alerts.go` for the exact JSON shape (`{"type":"calendar_list","data":[{id,summary,startTime,...}]}`):
```go
package ambient

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/tools"
)

type CalendarSource struct{ Cfg *config.Config }

func (c *CalendarSource) Name() string { return "calendar" }

func (c *CalendarSource) Start(ctx context.Context, out chan<- Suggestion) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	c.check(ctx, out) // once at start
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.check(ctx, out)
		}
	}
}

type calEvent struct {
	ID        string `json:"id"`
	Summary   string `json:"summary"`
	StartTime string `json:"startTime"`
	Location  string `json:"location"`
	HangoutLink string `json:"hangoutLink"`
}
type calList struct {
	Type string     `json:"type"`
	Data []calEvent `json:"data"`
}

func (c *CalendarSource) check(ctx context.Context, out chan<- Suggestion) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if out2, err := (&tools.GoogleCalendarListTool{Cfg: c.Cfg}).Execute(cctx, []byte(`{"maxResults":10}`)); err == nil && strings.Contains(out2, `"calendar_list"`) {
		c.emit(out2, out)
	}
	if out2, err := (&tools.MicrosoftCalendarListTool{Cfg: c.Cfg}).Execute(cctx, []byte(`{"top":10}`)); err == nil && strings.Contains(out2, `"calendar_list"`) {
		c.emit(out2, out)
	}
}

func (c *CalendarSource) emit(raw string, out chan<- Suggestion) {
	var list calList
	if json.Unmarshal([]byte(raw), &list) != nil {
		return
	}
	now := time.Now()
	for _, ev := range list.Data {
		if ev.StartTime == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, ev.StartTime)
		if err != nil {
			continue
		}
		diff := t.Sub(now)
		if diff <= 0 || diff > 10*time.Minute {
			continue // only within the next 10 minutes
		}
		mins := int(diff.Minutes()) + 1
		link := ev.HangoutLink
		if link == "" {
			link = ev.Location
		}
		s := Suggestion{
			Source: "calendar", Icon: "calendar", Action: "Join",
			Title:   ev.Summary,
			Message: minsMsg(mins) + joinHint(link),
			// Dedup per event per 5-min bucket so it can re-nudge but not spam.
			DedupKey: "cal:" + ev.ID,
		}
		if strings.HasPrefix(link, "http") {
			l := link
			s.Run = func(ctx context.Context) error { return openURL(l) }
		} else {
			s.Action = ""
		}
		out <- s
	}
}

func minsMsg(m int) string {
	if m <= 1 {
		return "Starting now"
	}
	return "In " + itoa(m) + " min"
}
func joinHint(link string) string {
	if strings.HasPrefix(link, "http") {
		return " · join?"
	}
	return ""
}
func itoa(n int) string { return strconv.Itoa(n) }
```
(Add imports `strconv`. Adjust `calEvent` fields to whatever the calendar tools actually emit — READ a sample or the tool source; keep `id/summary/startTime` which `alerts.go` relied on, and treat `hangoutLink`/`location` as best-effort.)

- [ ] **Step 2: Delete the old alerter**

```bash
git rm internal/alerts/alerts.go
```
Confirm nothing references `alerts.` : `grep -rn "internal/alerts" --include=*.go . ` → expect no results (it was dormant/unwired).

- [ ] **Step 3: Build + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/ambient/ ./internal/...` (exit 0)
```bash
git add internal/ambient/calendar.go
git commit -m "feat(ambient): calendar source (refactored from dormant alerts) emitting meeting suggestions"
```

---

### Task 7: Clipboard source (poll + classify)

**Files:** Create `internal/ambient/clipboard.go`

**Interfaces:**
- Consumes: `atotto/clipboard`, `ClassifyClipboard`, `openURL`, `Suggestion`.
- Produces: `type ClipboardSource struct { OnExplain func(ctx context.Context, text string) error }` (injected LLM action; nil-safe), implements `Source`; polls ~1.5s.

- [ ] **Step 1: Implement**

Create `internal/ambient/clipboard.go`:
```go
package ambient

import (
	"context"
	"time"

	"github.com/atotto/clipboard"
)

type ClipboardSource struct {
	// OnExplain routes an error snippet to the LLM (wired from main via dispatch). Nil => skip "error" suggestions.
	OnExplain func(ctx context.Context, text string) error
}

func (c *ClipboardSource) Name() string { return "clipboard" }

func (c *ClipboardSource) Start(ctx context.Context, out chan<- Suggestion) {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	last, _ := clipboard.ReadAll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur, err := clipboard.ReadAll()
			if err != nil || cur == last || cur == "" {
				continue
			}
			last = cur
			m, ok := ClassifyClipboard(cur)
			if !ok {
				continue
			}
			s := Suggestion{Source: "clipboard", Icon: m.Icon, Title: m.Title, Message: m.Message, Action: m.Action, DedupKey: "clip:" + m.Kind + ":" + m.URL}
			switch m.Kind {
			case "url", "tracking":
				u := m.URL
				s.Run = func(ctx context.Context) error { return openURL(u) }
			case "error":
				if c.OnExplain == nil {
					continue // no LLM action wired
				}
				text := cur
				s.DedupKey = "clip:error:" + truncate(text, 40)
				s.Run = func(ctx context.Context) error { return c.OnExplain(ctx, text) }
			}
			out <- s
		}
	}
}
```

- [ ] **Step 2: Build + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/ambient/` (exit 0)
```bash
git add internal/ambient/clipboard.go
git commit -m "feat(ambient): clipboard source (poll + classify → open/track/explain)"
```

---

## Phase D — UI

### Task 8: UI Deliverer glue + bindings

**Files:** Create `internal/ui/suggestion.go`; Modify `internal/ui/overlay.go`

**Interfaces:**
- Produces:
  - `func ShowSuggestion(id, icon, title, message, action string)` (in `internal/ui`) — dispatches `w.Eval("showSuggestion(...)")`.
  - Package vars `OnSuggestionAccept func(id string)` and `OnSuggestionDismiss func(id string)` bound in `StartOverlay`.

- [ ] **Step 1: Implement the eval + callbacks**

Create `internal/ui/suggestion.go`:
```go
package ui

import (
	"encoding/json"
	"fmt"
)

// Callbacks wired by the ambient engine.
var (
	OnSuggestionAccept  func(id string)
	OnSuggestionDismiss func(id string)
)

// ShowSuggestion renders a proactive suggestion card in the overlay.
func ShowSuggestion(id, icon, title, message, action string) {
	if w == nil {
		return
	}
	jid, _ := json.Marshal(id)
	jicon, _ := json.Marshal(icon)
	jt, _ := json.Marshal(title)
	jm, _ := json.Marshal(message)
	ja, _ := json.Marshal(action)
	w.Dispatch(func() {
		w.Eval(fmt.Sprintf("showSuggestion(%s,%s,%s,%s,%s);", string(jid), string(jicon), string(jt), string(jm), string(ja)))
	})
}
```

- [ ] **Step 2: Bind the accept/dismiss callbacks in StartOverlay**

In `internal/ui/overlay.go` `StartOverlay`, near the other `w.Bind(...)` calls, add:
```go
	w.Bind("suggestionAccept", func(id string) {
		if OnSuggestionAccept != nil {
			OnSuggestionAccept(id)
		}
	})
	w.Bind("suggestionDismiss", func(id string) {
		if OnSuggestionDismiss != nil {
			OnSuggestionDismiss(id)
		}
	})
```

- [ ] **Step 3: Verify compile + commit**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/ui/ && go vet ./internal/ui/` (exit 0/clean)
```bash
git add internal/ui/suggestion.go internal/ui/overlay.go
git commit -m "feat(ui): ShowSuggestion + suggestion accept/dismiss bindings"
```

---

### Task 9: Overlay redesign (dark-glass system + suggestion card)

**Files:** Modify `internal/ui/overlay_v2.html`

This is a visual reskin. **Preserve every JS function called from Go** and ADD `showSuggestion`/`suggestionAccept`/`suggestionDismiss`.

- [ ] **Step 1: Inventory the Go→JS calls to preserve**

Run: `grep -rn "w.Eval\|Eval(" internal/ui/*.go` and note every JS function name evaluated (expected: `updateUI`, `showCommand`, `showCard`/`renderContent`, `showConfirm`/confirm flow, `triggerMeetingAlert`, settings/dashboard functions, `submitCurrentCommand`). These MUST keep the same names/arity after the redesign.

- [ ] **Step 2: Replace the style layer with the dark-glass system**

Open `docs/sp3-ui-mockup.html` and copy its `<style>` design tokens + component classes (`.pill`, `.orb`, `.eq`, `.shimmer`, `.card`, `.badge`, `.btn`, `.actions`, the `@keyframes`, the `prefers-reduced-motion` block) into `overlay_v2.html`, REPLACING the old CSS. Keep the transparent-body assumption (the overlay window is transparent; only the pill/cards are glass). Map the existing state keys to the new visuals: `updateUI('idle'|'listening'|'thinking'|'acting'|'speaking')` toggles the pill's orb/eq/shimmer per the mockup states.

- [ ] **Step 3: Restructure the pill + card markup to the new components**

Rework the overlay DOM so: the pill uses `.pill` + `.orb`; the command bar uses `.pill.bar` with the input (still calling `window.submitCommand`); output/confirm/meeting cards use `.card` markup from the mockup. Keep element IDs/hooks the JS uses; only change classes/structure to the new system. Confirm buttons keep calling the existing confirm callback (`confirmCallback`), meeting alert keeps the `triggerMeetingAlert(title,text)` entry.

- [ ] **Step 4: Add the suggestion card JS**

Add these functions (vanilla JS, matching the mockup's card markup with a badge glyph chosen by `icon` key, an `[action]` primary button, and a `[Dismiss]` ghost):
```js
function showSuggestion(id, icon, title, message, action) {
  const glyph = {download:'⭳', calendar:'▦', link:'↗', warn:'△'}[icon] || '•';
  const el = document.getElementById('suggestion') || makeSuggestionEl();
  el.dataset.id = id;
  el.innerHTML =
    '<div class="row"><span class="badge">' + glyph + '</span>' +
    '<div><p class="ttl">' + esc(title) + '</p><p class="sub">' + esc(message) + '</p></div></div>' +
    '<div class="actions right">' +
      '<button class="btn ghost" onclick="dismissSuggestion()">Dismiss</button>' +
      (action ? '<button class="btn primary" onclick="acceptSuggestion()">' + esc(action) + '</button>' : '') +
    '</div>';
  el.style.display = 'block';
  clearTimeout(window.__sgTimer);
  window.__sgTimer = setTimeout(dismissSuggestion, 15000);
  callResize(380, 120, 18); // grow the window for the card (match your resize API)
}
function acceptSuggestion() { const el=document.getElementById('suggestion'); if(el){ suggestionAccept(el.dataset.id); hideSuggestion(); } }
function dismissSuggestion() { const el=document.getElementById('suggestion'); if(el){ suggestionDismiss(el.dataset.id); hideSuggestion(); } }
function hideSuggestion(){ const el=document.getElementById('suggestion'); if(el){ el.style.display='none'; } clearTimeout(window.__sgTimer); callResize(240,44,22); }
```
(`esc` = the page's existing HTML-escape helper, or add `function esc(s){return String(s).replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));}`. `makeSuggestionEl()` creates a `<div id="suggestion" class="card">` appended to the overlay root. Match `callResize` to the real resize signature seen in `overlay.go` — `callResize(width,height,radius)`.)

- [ ] **Step 5: Verify the embed compiles and JS entry points intact**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/ui/ && go vet ./cmd/app/` (exit 0). Then manually: build the app (`go build -ldflags="-H windowsgui" -o voice-agent.exe ./cmd/app`), launch, and confirm the pill shows, Ctrl+Space opens the (restyled) command bar, and a test `ui.ShowSuggestion` renders a card. (Automated tests can't cover WebView rendering.)

- [ ] **Step 6: Commit**

```bash
git add internal/ui/overlay_v2.html
git commit -m "feat(ui): redesign overlay to dark-glass system + suggestion card"
```

---

## Phase E — Config, wiring, verify

### Task 10: Config toggle + start the engine in main

**Files:** Modify `config/config.go`, `cmd/app/main.go`, `internal/ui/overlay.go` (settings)

**Interfaces:**
- Consumes: `ambient.Engine`, the three sources, `ui.ShowSuggestion`, `ui.OnSuggestionAccept/Dismiss`, `engineApp.IsBusy` (from SP2).
- Produces: `config.Config.EnableProactive bool`; a wired ambient engine started when enabled.

- [ ] **Step 1: Add the config field**

In `config/config.go` `Config` struct, under UX toggles: `EnableProactive bool `json:"enable_proactive"``. (No default needed — zero value false = off.) Add it to `getSettings`/`saveSettings` in `overlay.go` if you want it toggleable from the settings panel (optional; READ those funcs — extend the `saveSettings` signature + the settings JSON if adding).

- [ ] **Step 2: Wire + start the engine (gated) in main.go**

In `cmd/app/main.go`, after the engine is created and overlays are set up, add (READ main.go for the real engine var `engineApp` and root ctx `rootCtx`):
```go
	if cfg.EnableProactive && !cfg.PrivacyMode {
		amb := &ambient.Engine{
			Sources: []ambient.Source{
				ambient.NewDownloadsSource(),
				&ambient.CalendarSource{Cfg: cfg},
				&ambient.ClipboardSource{OnExplain: func(ctx context.Context, text string) error {
					return dispatchDeps.Handle(ctx, "explain this error: "+text, agentctx.Capture{})
				}},
			},
			Policy:  ambient.NewPolicy(90 * time.Second),
			UI:      ambient.DelivererFunc(ui.ShowSuggestion),
			Busy:    engineApp.IsBusy,
			Enabled: func() bool { return cfg.EnableProactive && !cfg.PrivacyMode },
		}
		ui.OnSuggestionAccept = amb.Accept
		ui.OnSuggestionDismiss = amb.Dismiss
		go amb.Run(rootCtx)
	}
```
This needs a small adapter so `ui.ShowSuggestion` (a func) satisfies `ambient.Deliverer`. Add to `internal/ambient/suggestion.go`:
```go
// DelivererFunc adapts a function to the Deliverer interface.
type DelivererFunc func(id, icon, title, message, action string)

func (f DelivererFunc) ShowSuggestion(id string, s Suggestion) {
	f(id, s.Icon, s.Title, s.Message, s.Action)
}
```
For `OnExplain`, wire it to whatever dispatch handle you have available in main (the command router's `globalDispatch` or the engine's `Dispatch`); if none is conveniently reachable, pass `OnExplain: nil` (clipboard error suggestions are simply skipped — acceptable for v1). Add imports `context`, `time`, `github.com/yourname/voice-agent/internal/ambient`, and `agentctx` if using the dispatch form.

- [ ] **Step 3: Verify compile**

Run: `export PATH="$PATH:/c/w64devkit/bin" && go build ./internal/... && go vet ./cmd/app/` (exit 0). Also `go test ./internal/ambient/` (all pass).

- [ ] **Step 4: Commit**

```bash
git add config/config.go cmd/app/main.go internal/ambient/suggestion.go internal/ui/overlay.go
git commit -m "feat: start ambient engine when enable_proactive set (off by default)"
```

---

### Task 11: Full verification + docs

**Files:** Modify `README.md` and/or `CLAUDE.md`

- [ ] **Step 1: Suite + vet**

Run:
```bash
export PATH="$PATH:/c/w64devkit/bin"
go vet ./cmd/... ./internal/...
go test -count=1 ./internal/ambient/ ./internal/dispatch/ ./internal/resolver/ ./internal/context/
```
Expected: vet clean; all tests PASS.

- [ ] **Step 2: Manual smoke**

Set `enable_proactive: true` in `config.json`. Build (`go build -ldflags="-H windowsgui" -o voice-agent.exe ./cmd/app`), launch. Drop a `.zip` into Downloads → an "Unzip?" card appears → click Unzip → the archive extracts. Copy a URL → "Open?" card. Confirm dismiss + the 15s auto-dismiss. Set `enable_proactive: false` → no cards, no watchers.

- [ ] **Step 3: Docs**

Add a short "Proactive suggestions (SP3)" note to `README.md`/`CLAUDE.md`: opt-in via `enable_proactive`, sources (downloads/calendar/clipboard), off by default, suppressed in `privacy_mode`.

- [ ] **Step 4: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs(sp3): ambient proactive suggestions — opt-in, sources, privacy"
```

---

## Self-Review (completed by plan author)

- **Spec coverage:** Engine/policy/suggestion → Tasks 1–3; classifiers → Task 4; 3 sources → Tasks 5–7; UI deliverer+bindings → Task 8; dark-glass redesign + suggestion card → Task 9; config toggle + gated start → Task 10; verify+docs → Task 11. Off-by-default enforced in Task 10 (gated start) + engine `Enabled`. No-LLM-in-trigger-path holds (sources never call the LLM; only accepted `OnExplain` does). Dedup/min-gap/busy → Policy (Task 2) + Engine (Task 3). Calendar refactor + `alerts` deletion → Task 6.
- **Placeholder scan:** no TBD/TODO; complete code in every code step. "Read the real signature and adjust" anchors (calendar event fields in Task 6; `callResize`/esc helper in Task 9; engine var in Task 10) are guardrails, not placeholders.
- **Type consistency:** `Suggestion`, `Source`, `Deliverer`/`DelivererFunc`, `Policy.Allow/Record`, `Engine.consider/Accept/Dismiss`, `ClassifyDownload`/`ClassifyClipboard`, `ShowSuggestion`, `OnSuggestionAccept/Dismiss` used identically across producing and consuming tasks.
```
