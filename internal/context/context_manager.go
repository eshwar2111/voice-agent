package context

import "time"

type AgentContext struct {
	Window    *WindowContext
	Clipboard string
	Time      time.Time
}

func BuildContext() *AgentContext {
	window, _ := GetActiveWindowContext()
	clip, _ := GetClipboardText()

	// Optionally truncate clipboard to avoid massive context bloating
	if len(clip) > 1000 {
		clip = clip[:1000] + "... (truncated)"
	}

	return &AgentContext{
		Window:    window,
		Clipboard: clip,
		Time:      time.Now(),
	}
}
