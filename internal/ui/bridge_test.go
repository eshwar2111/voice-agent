package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// The envelope must survive payloads that used to break string interpolation.
// Regression for d4e5cdd ("approval card showed [object Object]").
func TestEnvelopeHostilePayloads(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		payload any
	}{
		{"double quotes", "notify", map[string]string{"text": `he said "hi"`}},
		{"single quotes", "notify", map[string]string{"text": `it's fine`}},
		{"newlines", "notify", map[string]string{"text": "line1\nline2\r\nline3"}},
		{"script close tag", "notify", map[string]string{"text": "</script><script>evil()</script>"}},
		{"backslash", "notify", map[string]string{"text": `C:\Users\Eshwar\file.txt`}},
		{"emoji", "notify", map[string]string{"text": "done ✅ 🎉"}},
		{"invalid utf8", "notify", map[string]string{"text": "bad\xff\xfebytes"}},
		{"nested object", "activity:update", map[string]any{
			"id": "trust.approval", "data": map[string]any{"steps": []string{"a", "b"}}}},
		{"nil payload", "surface:close", nil},
	}

	for _, tc := range cases {
		js, err := envelope(tc.kind, tc.payload)
		if err != nil {
			t.Errorf("%s: envelope returned error: %v", tc.name, err)
			continue
		}
		// Nothing that could terminate the surrounding script context may survive.
		if strings.Contains(js, "</script") {
			t.Errorf("%s: raw </script survived into JS: %s", tc.name, js)
		}
		if strings.ContainsAny(js, "\n\r") {
			t.Errorf("%s: raw newline survived into JS: %q", tc.name, js)
		}
		// The argument must be valid JSON that round-trips to the same kind.
		open := strings.Index(js, "(")
		closeIdx := strings.LastIndex(js, ")")
		if open < 0 || closeIdx < open {
			t.Fatalf("%s: cannot find call arguments in %q", tc.name, js)
		}
		var got struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(js[open+1:closeIdx]), &got); err != nil {
			t.Errorf("%s: argument is not valid JSON: %v (%s)", tc.name, err, js)
			continue
		}
		if got.Kind != tc.kind {
			t.Errorf("%s: kind = %q, want %q", tc.name, got.Kind, tc.kind)
		}
	}
}

func TestEnvelopeCallsRecv(t *testing.T) {
	js, err := envelope("state", map[string]string{"state": "listening"})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if !strings.Contains(js, "__agent") || !strings.Contains(js, "recv") {
		t.Errorf("envelope does not call __agent.recv: %s", js)
	}
}

func TestBridgeBuffersUntilReady(t *testing.T) {
	var sent []string
	b := &Bridge{eval: func(js string) { sent = append(sent, js) }}

	b.Push("state", map[string]string{"state": "listening"})
	b.Push("state", map[string]string{"state": "thinking"})
	if len(sent) != 0 {
		t.Fatalf("pushed %d evals before Ready, want 0", len(sent))
	}

	b.Ready()
	if len(sent) != 2 {
		t.Fatalf("flushed %d evals, want 2", len(sent))
	}
	if !strings.Contains(sent[0], "listening") || !strings.Contains(sent[1], "thinking") {
		t.Errorf("flush lost ordering: %v", sent)
	}

	b.Push("state", map[string]string{"state": "idle"})
	if len(sent) != 3 {
		t.Errorf("post-Ready push was buffered instead of sent immediately")
	}
}

// TestBridgeFlushOrderingUnderRace is a regression test for a race in an
// earlier version of Ready(): it set ready=true, unlocked, and only then
// looped over the buffered items calling eval. A concurrent Push arriving in
// that unlocked window saw ready==true and took the "send immediately" path,
// so it could reach the WebView before some of the buffered messages —
// breaking the "flushed in order" guarantee. Ordering must be structural
// (guarded by the single-drainer invariant), not an artifact of how fast
// Ready's loop happens to run.
//
// eval sleeps briefly to widen the race window; without that a fast machine
// can make even the buggy implementation happen to pass by luck, which is
// why -race -count=20 is the prescribed way to run this.
func TestBridgeFlushOrderingUnderRace(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	b := &Bridge{eval: func(js string) {
		time.Sleep(time.Millisecond)
		mu.Lock()
		sent = append(sent, js)
		mu.Unlock()
	}}

	const n = 20
	for i := 0; i < n; i++ {
		b.Push("state", map[string]string{"state": fmt.Sprintf("buffered-%d", i)})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); b.Ready() }()
	go func() { defer wg.Done(); b.Push("state", map[string]string{"state": "racer"}) }()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	if len(sent) != n+1 {
		t.Fatalf("got %d evals, want %d", len(sent), n+1)
	}
	racerIdx := -1
	for i, js := range sent {
		if strings.Contains(js, "racer") {
			racerIdx = i
		}
	}
	if racerIdx != n {
		t.Errorf("racer push landed at index %d, want last (%d) — buffered pushes were not flushed in order: %v", racerIdx, n, sent)
	}
}
