package ui

import (
	"encoding/json"
	"log"
	"sync"

	webview "github.com/webview/webview_go"
)

// envelope renders one Go->JS message as a JavaScript call.
//
// The entire payload is marshalled as a single JSON value, so no caller-supplied
// string ever becomes JavaScript source. Go's json.Marshal escapes <, > and &
// to \u003c, \u003e and \u0026 by default, which is exactly what keeps a
// payload containing "</script>" from terminating the script context.
func envelope(kind string, payload any) (string, error) {
	env := struct {
		Kind string `json:"kind"`
		Data any    `json:"data,omitempty"`
	}{Kind: kind, Data: payload}

	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return "window.__agent&&window.__agent.recv(" + string(raw) + ")", nil
}

// Bridge is the single Go->JS channel. Pushes made before the page has loaded
// are buffered and flushed in order once JS calls uiReady — replacing the
// time.Sleep(250ms) race that used to guard startup.
//
// Ordering is structural, not timing-dependent: every push (buffered or live)
// goes through the same buf, and at most one goroutine ever drains it at a
// time (guarded by draining). A Push that arrives while a drain is already in
// flight just appends and returns — the active drainer rechecks buf under the
// lock on every iteration, so it will pick up the new item in FIFO order
// instead of racing it to the WebView. eval is never called while b.mu is
// held, since it dispatches into the WebView and could re-enter.
type Bridge struct {
	mu       sync.Mutex
	ready    bool
	draining bool
	buf      []string
	eval     func(js string) // injectable for tests
}

func newBridge(w webview.WebView) *Bridge {
	return &Bridge{eval: func(js string) { w.Dispatch(func() { w.Eval(js) }) }}
}

func (b *Bridge) Push(kind string, payload any) {
	if b == nil {
		return
	}
	js, err := envelope(kind, payload)
	if err != nil {
		log.Printf("[ui/bridge] marshal %s: %v", kind, err)
		return
	}
	b.mu.Lock()
	b.buf = append(b.buf, js)
	if !b.ready || b.draining {
		b.mu.Unlock()
		return
	}
	b.draining = true
	b.mu.Unlock()
	b.drain()
}

func (b *Bridge) Ready() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.ready = true
	if b.draining || len(b.buf) == 0 {
		b.mu.Unlock()
		return
	}
	b.draining = true
	b.mu.Unlock()
	b.drain()
}

// drain pops the head of buf and evals it, one at a time, until buf is empty.
// Callers must hold no lock and must have already claimed b.draining == true.
func (b *Bridge) drain() {
	for {
		b.mu.Lock()
		if len(b.buf) == 0 {
			b.draining = false
			b.mu.Unlock()
			return
		}
		js := b.buf[0]
		b.buf = b.buf[1:]
		b.mu.Unlock()
		b.eval(js)
	}
}
