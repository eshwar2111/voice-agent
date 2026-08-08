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

// TestMeetingSignificantOnlyAtThresholds runs a genuine descending sequence
// against ONE meeting instance (fixed StartsAt, the clock advances), exactly
// as a real countdown would poll. It does not reset m.lastWake between
// cases: that reset previously masked a re-fire bug (see
// TestMeetingWakesOncePerThreshold) by making every case start fresh. The
// clock — not StartsAt — is what advances, so this instance is never
// mistaken for a new meeting by the instance-scoped reset in activityFor.
func TestMeetingSignificantOnlyAtThresholds(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)
	startsAt := clk.t.Add(30 * time.Minute)
	n := &NextMeeting{Title: "Standup", StartsAt: startsAt}

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
	elapsed := 0
	for _, tc := range cases {
		clk.advance(time.Duration(30-tc.minutes-elapsed) * time.Minute)
		elapsed = 30 - tc.minutes
		a, ok := m.activityFor(n)
		if !ok {
			t.Fatalf("%dm: activityFor returned ok=false", tc.minutes)
		}
		if a.Significant != tc.want {
			t.Errorf("%dm: Significant = %v, want %v", tc.minutes, a.Significant, tc.want)
		}
	}
}

// TestMeetingWakesOnSkippedThreshold covers a poll that jumps past T-5m (e.g.
// the machine was briefly busy or asleep, so the first observation lands at
// 4 minutes rather than exactly 5). The T-5 warning must still fire — late
// rather than never — for a fresh provider.
func TestMeetingWakesOnSkippedThreshold(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	a, ok := m.activityFor(&NextMeeting{
		Title: "Standup", StartsAt: clk.t.Add(4 * time.Minute),
	})
	if !ok {
		t.Fatal("activityFor returned ok=false")
	}
	if !a.Significant {
		t.Error("a poll that skips T-5m and first observes at 4m should still be Significant")
	}
}

// TestMeetingNoRepeatAtZero covers the bug the old test's per-case lastWake
// reset was hiding: with a descending wakeThresholds slice and a first-match
// loop, a meeting sitting at 0 minutes could re-fire Significant forever,
// because 0 <= 5 matches the 5-minute threshold first on every poll after the
// 0/1 thresholds were already consumed. Two consecutive observations at 0
// minutes must wake only once.
func TestMeetingNoRepeatAtZero(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)
	n := &NextMeeting{Title: "Standup", StartsAt: clk.t}

	first, ok := m.activityFor(n)
	if !ok {
		t.Fatal("first: activityFor returned ok=false")
	}
	if !first.Significant {
		t.Error("first observation at 0 minutes should be Significant")
	}

	second, ok := m.activityFor(n)
	if !ok {
		t.Fatal("second: activityFor returned ok=false")
	}
	if second.Significant {
		t.Error("second observation at 0 minutes must NOT re-wake the island")
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

// TestMeetingBackToBackMeetingsEachWake is the regression test for the bug
// where lastWake was scoped to the provider instead of the meeting instance:
// meeting A wakes at T-0, then the source hands the provider meeting B
// (a different StartsAt) already 25 minutes out. B must get its own full
// threshold sequence rather than inheriting A's lastWake=0, which would
// otherwise suppress every one of B's wakes (5<0, 1<0, 0<0 are all false).
func TestMeetingBackToBackMeetingsEachWake(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	a := &NextMeeting{Title: "A", StartsAt: clk.t}
	firstA, ok := m.activityFor(a)
	if !ok {
		t.Fatal("A: activityFor returned ok=false")
	}
	if !firstA.Significant {
		t.Fatal("A at 0 minutes should be Significant")
	}

	b := &NextMeeting{Title: "B", StartsAt: clk.t.Add(25 * time.Minute)}
	if _, ok := m.activityFor(b); !ok {
		t.Fatal("B at 25 minutes: activityFor returned ok=false")
	}

	atFive, ok := m.activityFor(&NextMeeting{Title: "B", StartsAt: clk.t.Add(5 * time.Minute)})
	if !ok {
		t.Fatal("B at 5 minutes: activityFor returned ok=false")
	}
	if !atFive.Significant {
		t.Error("B crossing T-5m must wake the island — back-to-back meetings must not go silent")
	}
}

// TestMeetingSameInstanceDoesNotResetOnEveryCall confirms the instance-scoped
// reset triggers only on a genuinely different StartsAt, not on every call —
// repeated polls of the same meeting must still wake once per threshold, not
// once per poll.
func TestMeetingSameInstanceDoesNotResetOnEveryCall(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)
	n := &NextMeeting{Title: "Standup", StartsAt: clk.t.Add(5 * time.Minute)}

	first, ok := m.activityFor(n)
	if !ok || !first.Significant {
		t.Fatal("first poll at T-5m should be Significant")
	}
	// Same instance, same minute, polled again — must not reset/re-wake.
	second, ok := m.activityFor(n)
	if !ok {
		t.Fatal("second poll: activityFor returned ok=false")
	}
	if second.Significant {
		t.Error("polling the same meeting instance again must not re-wake the island")
	}
}

// TestMeetingReplacedByEarlierMeeting covers a meeting just added to the
// calendar ahead of what the provider was tracking: A at 40 minutes (below
// any threshold, so no wake yet), then the source swaps to B at 3 minutes.
// B must wake immediately rather than being suppressed by A's bookkeeping.
func TestMeetingReplacedByEarlierMeeting(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	a := &NextMeeting{Title: "A", StartsAt: clk.t.Add(40 * time.Minute)}
	firstA, ok := m.activityFor(a)
	if !ok {
		t.Fatal("A: activityFor returned ok=false")
	}
	if firstA.Significant {
		t.Fatal("A at 40 minutes should not be Significant yet")
	}

	b := &NextMeeting{Title: "B", StartsAt: clk.t.Add(3 * time.Minute)}
	atThree, ok := m.activityFor(b)
	if !ok {
		t.Fatal("B: activityFor returned ok=false")
	}
	if !atThree.Significant {
		t.Error("B, newly added at 3 minutes out, must wake immediately")
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
