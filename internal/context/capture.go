package context

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/go-vgo/robotgo"
	"github.com/yourname/voice-agent/internal/executor"
)

// Capture is the ambient desktop context attached to a Tier-1 request.
type Capture struct {
	AppName     string
	WindowTitle string
	Clipboard   string
	Selection   string
	Screenshot  []byte
}

const clipMax = 2000

// Overridable grabbers (replaced in tests).
var (
	grabWindow = func() (string, string) {
		wc, err := GetActiveWindowContext()
		if err != nil || wc == nil {
			return "", ""
		}
		return wc.ProcessName, wc.WindowTitle
	}
	grabClipboard = func() string {
		s, _ := clipboard.ReadAll()
		return s
	}
	// grabSelection copies the current selection WITHOUT clobbering the clipboard.
	grabSelection = func() string {
		saved, _ := clipboard.ReadAll()
		robotgo.KeyTap("c", "ctrl")
		time.Sleep(80 * time.Millisecond) // short settle; keep the bar snappy
		sel, _ := clipboard.ReadAll()
		_ = clipboard.WriteAll(saved) // restore, best-effort
		if sel == saved {
			return "" // nothing newly selected
		}
		return sel
	}
	grabScreen = func() []byte {
		b, err := executor.CaptureScreen()
		if err != nil {
			return nil
		}
		return b
	}
)

func truncate(s string) string {
	if len(s) > clipMax {
		return s[:clipMax] + "… (truncated)"
	}
	return s
}

// CaptureAmbient grabs window + clipboard always; selection only when withSelection.
func CaptureAmbient(withSelection bool) Capture {
	app, title := grabWindow()
	c := Capture{AppName: app, WindowTitle: title, Clipboard: truncate(grabClipboard())}
	if withSelection {
		c.Selection = truncate(grabSelection())
	}
	return c
}

// WithScreenshot returns a copy with the current screen captured.
func (c Capture) WithScreenshot() Capture {
	c.Screenshot = grabScreen()
	return c
}

// String renders the text context block for the LLM system prompt (empty fields omitted).
func (c Capture) String() string {
	var b strings.Builder
	add := func(label, v string) {
		if strings.TrimSpace(v) != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, v)
		}
	}
	add("Active App", c.AppName)
	add("Window Title", c.WindowTitle)
	add("Selected Text", c.Selection)
	add("Clipboard", c.Clipboard)
	return strings.TrimSpace(b.String())
}
