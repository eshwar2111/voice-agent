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
