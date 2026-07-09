package ambient

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/tools"
)

// CalendarSource polls Google/Microsoft calendars and emits meeting-join Suggestions
// for events starting within the next 10 minutes. Refactored from the dormant
// internal/alerts meeting alerter (SP3).
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

// calEvent mirrors the JSON shape emitted by GoogleCalendarListTool / MicrosoftCalendarListTool.
// The tools emit "joinLink" (not "hangoutLink") and "location"; both are best-effort and may be empty
// (the Microsoft tool in particular only populates id/summary/startTime today).
type calEvent struct {
	ID        string `json:"id"`
	Summary   string `json:"summary"`
	StartTime string `json:"startTime"`
	Location  string `json:"location"`
	JoinLink  string `json:"joinLink"`
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
		link := ev.JoinLink
		if link == "" {
			link = ev.Location
		}
		s := Suggestion{
			Source: "calendar", Icon: "calendar", Action: "Join",
			Title:   ev.Summary,
			Message: minsMsg(mins) + joinHint(link),
			// Dedup per event so it can re-nudge but not spam.
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
	return "In " + strconv.Itoa(m) + " min"
}
func joinHint(link string) string {
	if strings.HasPrefix(link, "http") {
		return " · join?"
	}
	return ""
}
