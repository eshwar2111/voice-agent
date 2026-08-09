package island

import (
	"context"
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

// TestMeetingLookaheadBoundaryInclusive pins the LookaheadMinutes edge: a
// meeting exactly 60 minutes out is still shown (the guard is `mins >
// LookaheadMinutes`, which excludes only strictly-above-60).
func TestMeetingLookaheadBoundaryInclusive(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	if _, ok := m.activityFor(&NextMeeting{Title: "Edge", StartsAt: clk.t.Add(60 * time.Minute)}); !ok {
		t.Error("a meeting exactly 60 minutes out (the lookahead boundary) should be included")
	}
}

// The following tests drive Run's transition logic through the poll method
// directly (repeated calls, no real ticker), per the coordinator's guidance
// that either driving Run with a short context or factoring poll out is
// acceptable as long as it's deterministic and doesn't sleep.

// TestRunBlipDoesNotReplaySequence is the regression test for the bug where
// Run's end() branch reset lastWake/lastMeeting on ANY ok=false, including a
// single transient poll failure (e.g. MeetingSource returning nil due to a
// flaky calendar sync). That reset made the very next poll of the SAME
// meeting look like a brand new instance and replay the whole T-5/T-1/T-0
// sequence. Sequence: meeting at T-5 (wakes) -> source blips to nil (end
// called) -> same meeting reappears at T-5 again -> must NOT re-wake.
func TestRunBlipDoesNotReplaySequence(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	n := &NextMeeting{Title: "Standup", StartsAt: clk.t.Add(5 * time.Minute)}

	calls := 0
	src := func(ctx context.Context) (*NextMeeting, error) {
		calls++
		switch calls {
		case 1:
			return n, nil // first poll: wakes at T-5
		case 2:
			return nil, nil // blip: source has nothing this round
		default:
			return n, nil // same meeting reappears
		}
	}
	m := NewMeetingProvider(clk, MeetingSource(src))

	var significants []bool
	var ended []string
	emit := func(a Activity) { significants = append(significants, a.Significant) }
	end := func(id string) { ended = append(ended, id) }
	ctx := context.Background()

	if err := m.poll(ctx, emit, end); err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	if err := m.poll(ctx, emit, end); err != nil {
		t.Fatalf("poll 2 (blip): %v", err)
	}
	if err := m.poll(ctx, emit, end); err != nil {
		t.Fatalf("poll 3 (same meeting returns): %v", err)
	}

	if len(significants) != 2 {
		t.Fatalf("expected 2 emits (poll 1 and poll 3), got %d: %v", len(significants), significants)
	}
	if !significants[0] {
		t.Error("poll 1 should be Significant (T-5 crossing)")
	}
	if significants[1] {
		t.Error("poll 3 (same meeting after a blip) must NOT replay the T-5 wake")
	}
	if len(ended) != 1 || ended[0] != "meeting.next" {
		t.Errorf("expected exactly one end(\"meeting.next\") for the blip, got %v", ended)
	}
}

// TestRunGenuineReplacementStillWakes drives the back-to-back-meetings case
// through Run's poll method (rather than activityFor directly) to confirm
// the fix holds at the Run layer too: no intervening nil poll, source just
// hands the provider a different meeting.
func TestRunGenuineReplacementStillWakes(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	a := &NextMeeting{Title: "A", StartsAt: clk.t}
	b := &NextMeeting{Title: "B", StartsAt: clk.t.Add(5 * time.Minute)}

	calls := 0
	src := func(ctx context.Context) (*NextMeeting, error) {
		calls++
		if calls == 1 {
			return a, nil
		}
		return b, nil // different StartsAt, no nil in between
	}
	m := NewMeetingProvider(clk, MeetingSource(src))

	var significants []bool
	emit := func(act Activity) { significants = append(significants, act.Significant) }
	end := func(string) {}
	ctx := context.Background()

	if err := m.poll(ctx, emit, end); err != nil {
		t.Fatalf("poll 1 (A at 0m): %v", err)
	}
	if err := m.poll(ctx, emit, end); err != nil {
		t.Fatalf("poll 2 (B at 5m): %v", err)
	}

	if len(significants) != 2 {
		t.Fatalf("expected 2 emits, got %d", len(significants))
	}
	if !significants[0] {
		t.Error("A at 0 minutes should be Significant")
	}
	if !significants[1] {
		t.Error("B, a genuinely different meeting at T-5m, must wake even with no intervening nil poll")
	}
}

// TestRunJitterDoesNotRewake covers sub-minute StartsAt jitter across polls
// for what is logically the same meeting (timezone re-normalization, source
// precision differences). It must not be treated as a new instance.
func TestRunJitterDoesNotRewake(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)}
	base := clk.t.Add(5 * time.Minute)
	jittered := base.Add(300 * time.Millisecond)

	calls := 0
	src := func(ctx context.Context) (*NextMeeting, error) {
		calls++
		startsAt := base
		if calls > 1 {
			startsAt = jittered
		}
		return &NextMeeting{Title: "Standup", StartsAt: startsAt}, nil
	}
	m := NewMeetingProvider(clk, MeetingSource(src))

	var significants []bool
	emit := func(a Activity) { significants = append(significants, a.Significant) }
	end := func(string) {}
	ctx := context.Background()

	if err := m.poll(ctx, emit, end); err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	if err := m.poll(ctx, emit, end); err != nil {
		t.Fatalf("poll 2 (jittered StartsAt): %v", err)
	}

	if len(significants) != 2 {
		t.Fatalf("expected 2 emits, got %d", len(significants))
	}
	if !significants[0] {
		t.Error("poll 1 (T-5 crossing) should be Significant")
	}
	if significants[1] {
		t.Error("a few hundred ms of StartsAt jitter must not look like a new meeting and re-wake")
	}
}

// Two genuinely different meetings can start in the same clock minute — a
// cancelled 12:00:00 replaced by a different 12:00:30 one. Minute-truncated
// timestamps collide there and suppress the second meeting's alerts entirely.
// A real calendar event ID cannot collide.
func TestMeetingDistinguishesSameMinuteByID(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	a := &NextMeeting{ID: "evt-A", Title: "Standup", StartsAt: clk.t.Add(5 * time.Minute)}
	first, _ := m.activityFor(a)
	if !first.Significant {
		t.Fatalf("first meeting crossing T-5m should wake")
	}

	// Different event, same clock minute, 30s later.
	b := &NextMeeting{ID: "evt-B", Title: "Review", StartsAt: clk.t.Add(5*time.Minute + 30*time.Second)}
	second, _ := m.activityFor(b)
	if !second.Significant {
		t.Errorf("a DIFFERENT meeting in the same clock minute must get its own wake — " +
			"minute-truncated identity collided here and silently suppressed it")
	}
}

// Same event re-fetched with jittered timestamps must NOT re-wake.
func TestMeetingSameIDDoesNotRewakeOnJitter(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)}
	m := NewMeetingProvider(clk, nil)

	a := &NextMeeting{ID: "evt-A", Title: "Standup", StartsAt: clk.t.Add(5 * time.Minute)}
	if first, _ := m.activityFor(a); !first.Significant {
		t.Fatalf("first crossing should wake")
	}
	jittered := &NextMeeting{ID: "evt-A", Title: "Standup", StartsAt: clk.t.Add(5*time.Minute + 300*time.Millisecond)}
	if second, _ := m.activityFor(jittered); second.Significant {
		t.Errorf("same event ID must not re-wake on a jittered re-fetch")
	}
}
