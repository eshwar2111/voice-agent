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
// to </>/& by default, which is exactly what keeps a payload
// containing "</script>" from terminating the script context.
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
type Bridge struct {
	mu    sync.Mutex
	ready bool
	buf   []string
	eval  func(js string) // injectable for tests
}

func newBridge(w webview.WebView) *Bridge {
	return &Bridge{eval: func(js string) { w.Dispatch(func() { w.Eval(js) }) }}
}

func (b *Bridge) Push(kind string, payload any) {
	js, err := envelope(kind, payload)
	if err != nil {
		log.Printf("[ui/bridge] marshal %s: %v", kind, err)
		return
	}
	b.mu.Lock()
	if !b.ready {
		b.buf = append(b.buf, js)
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	b.eval(js)
}

func (b *Bridge) Ready() {
	b.mu.Lock()
	b.ready = true
	pending := b.buf
	b.buf = nil
	b.mu.Unlock()
	for _, js := range pending {
		b.eval(js)
	}
}
