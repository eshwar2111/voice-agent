package island

import (
	"testing"
	"time"
)

func TestMeetingActivityFields(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	a, ok := m.activityFor(&NextMeeting{
		Title:    "Standup",
		JoinURL:  "https://meet.example/abc",
		StartsAt: clk.t.Add(5 * time.Minute),
	})
	if !ok {
		t.Fatal("activityFor returned ok=false for a valid upcoming meeting")
	}
	if a.ID != "meeting.next" {
		t.Errorf("ID = %q, want meeting.next", a.ID)
	}
	if a.Data["title"] != "Standup" {
		t.Errorf("title = %v", a.Data["title"])
	}
	if a.Data["minutes"] != 5 {
		t.Errorf("minutes = %v, want 5", a.Data["minutes"])
	}
	if a.Data["joinURL"] != "https://meet.example/abc" {
		t.Errorf("joinURL = %v", a.Data["joinURL"])
	}
}

func TestMeetingSignificantOnlyAtThresholds(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	cases := []struct {
		minutes int
		want    bool
	}{
		{30, false},
		{11, false},
		{5, true},  // T-5m
		{4, false}, // already woke at 5
		{1, true},  // T-1m
		{0, true},  // starting now
	}
	for _, tc := range cases {
		m.lastWake = -1 // reset between cases
		a, ok := m.activityFor(&NextMeeting{
			Title: "Standup", StartsAt: clk.t.Add(time.Duration(tc.minutes) * time.Minute),
		})
		if !ok {
			t.Fatalf("%dm: activityFor returned ok=false", tc.minutes)
		}
		if a.Significant != tc.want {
			t.Errorf("%dm: Significant = %v, want %v", tc.minutes, a.Significant, tc.want)
		}
	}
}

// Repeated polls at the same threshold must wake only once, or a 60s poll would
// re-wake the island every minute at T-5.
func TestMeetingWakesOncePerThreshold(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)
	n := &NextMeeting{Title: "Standup", StartsAt: clk.t.Add(5 * time.Minute)}

	first, _ := m.activityFor(n)
	second, _ := m.activityFor(n)

	if !first.Significant {
		t.Errorf("first crossing of T-5m should be Significant")
	}
	if second.Significant {
		t.Errorf("second poll at the same threshold must NOT re-wake the island")
	}
}

func TestMeetingIgnoresPastAndFarFuture(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	if _, ok := m.activityFor(nil); ok {
		t.Errorf("nil meeting produced an activity")
	}
	if _, ok := m.activityFor(&NextMeeting{Title: "Old", StartsAt: clk.t.Add(-time.Hour)}); ok {
		t.Errorf("a meeting an hour in the past produced an activity")
	}
	if _, ok := m.activityFor(&NextMeeting{Title: "Later", StartsAt: clk.t.Add(3 * time.Hour)}); ok {
		t.Errorf("a meeting 3h away produced an activity — the island is not a calendar")
	}
}
