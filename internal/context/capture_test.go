package context

import (
	"strings"
	"testing"
)

func withFakes(t *testing.T, app, title, clip, sel string) {
	t.Helper()
	ow, oc, os_ := grabWindow, grabClipboard, grabSelection
	grabWindow = func() (string, string) { return app, title }
	grabClipboard = func() string { return clip }
	grabSelection = func() string { return sel }
	t.Cleanup(func() { grabWindow, grabClipboard, grabSelection = ow, oc, os_ })
}

func TestCaptureAmbientWithSelection(t *testing.T) {
	withFakes(t, "chrome.exe", "Inbox - Gmail", "clip text", "selected text")
	c := CaptureAmbient(true)
	if c.AppName != "chrome.exe" || c.WindowTitle != "Inbox - Gmail" {
		t.Errorf("window not captured: %+v", c)
	}
	if c.Clipboard != "clip text" || c.Selection != "selected text" {
		t.Errorf("clip/sel not captured: %+v", c)
	}
	s := c.String()
	for _, want := range []string{"chrome.exe", "Inbox - Gmail", "clip text", "selected text"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() missing %q: %s", want, s)
		}
	}
}

func TestCaptureAmbientSkipsSelection(t *testing.T) {
	called := false
	withFakes(t, "a", "b", "c", "d")
	grabSelection = func() string { called = true; return "d" }
	if c := CaptureAmbient(false); c.Selection != "" || called {
		t.Errorf("withSelection=false must not grab selection (got %q, called=%v)", c.Selection, called)
	}
}
