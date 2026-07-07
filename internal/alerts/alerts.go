package alerts

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/tools"
	"github.com/yourname/voice-agent/internal/ui"
)

func StartMeetingAlerter(cfg *config.Config) {
	go func() {
		// Run every 5 minutes
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		warnedEvents := make(map[string]bool)

		for range ticker.C {
			checkMeetings(cfg, warnedEvents)
		}
	}()
}

func checkMeetings(cfg *config.Config, warned map[string]bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Check Google Calendar
	gcal := &tools.GoogleCalendarListTool{Cfg: cfg}
	out, err := gcal.Execute(ctx, []byte(`{"maxResults": 10}`))
	if err == nil && strings.Contains(out, `"calendar_list"`) {
		processCalendarJSON(out, warned)
	}

	// 2. Check Microsoft Calendar
	msft := &tools.MicrosoftCalendarListTool{Cfg: cfg}
	out2, err2 := msft.Execute(ctx, []byte(`{"top": 10}`))
	if err2 == nil && strings.Contains(out2, `"calendar_list"`) {
		processCalendarJSON(out2, warned)
	}
}

type calList struct {
	Type string `json:"type"`
	Data []struct {
		ID        string `json:"id"`
		Summary   string `json:"summary"`
		StartTime string `json:"startTime"`
	} `json:"data"`
}

func processCalendarJSON(raw string, warned map[string]bool) {
	var list calList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return
	}

	now := time.Now()

	for _, ev := range list.Data {
		if ev.StartTime == "" || warned[ev.ID] {
			continue
		}

		t, err := time.Parse(time.RFC3339, ev.StartTime)
		if err != nil {
			// Skip all-day events or badly formatted dates
			continue
		}

		diff := t.Sub(now)

		// Alert if between 10m and 15m away
		if diff > 10*time.Minute && diff <= 15*time.Minute {
			log.Printf("Alerter: Sending 15m alert for %s", ev.Summary)
			ui.SetMeetingAlert(ev.Summary, 15)
			warned[ev.ID] = true
		} else if diff > 0 && diff <= 5*time.Minute && !warned[ev.ID+"_5m"] {
			log.Printf("Alerter: Sending 5m alert for %s", ev.Summary)
			ui.SetMeetingAlert(ev.Summary, 5)
			warned[ev.ID+"_5m"] = true
		}

		// Cleanup old event IDs from map to prevent unbounded memory growth
		if diff < -24*time.Hour {
			delete(warned, ev.ID)
			delete(warned, ev.ID+"_5m")
		}
	}
}
