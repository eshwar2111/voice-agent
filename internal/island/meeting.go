package island

import (
	"context"
	"time"
)

// LookaheadMinutes bounds how early a meeting appears. The island shows what is
// imminent; it is not a calendar.
const LookaheadMinutes = 60

// wakeThresholds are the remaining-minute values worth interrupting for.
// Ascending, so the first match is the SMALLEST applicable threshold.
// Descending would match 5 for a meeting 0 minutes away and record the wrong
// bookkeeping, re-firing forever.
var wakeThresholds = []int{0, 1, 5}

type NextMeeting struct {
	Title    string
	JoinURL  string
	StartsAt time.Time
}

// MeetingSource fetches the next meeting. Injected so internal/island stays
// stdlib-only and the provider is testable without network access.
type MeetingSource func(ctx context.Context) (*NextMeeting, error)

type MeetingProvider struct {
	clock Clock
	src   MeetingSource
	// lastWake is the threshold already woken for, so a 60s poll does not
	// re-wake the island every minute at the same threshold. Scoped to
	// lastMeeting: a new meeting instance (different StartsAt, at minute
	// granularity) gets its own full T-5/T-1/start sequence, so back-to-back
	// meetings each still warn. It is intentionally NOT reset when the
	// activity ends in Run (see poll) — a transient source blip (a single
	// nil/empty poll) must not replay the alert sequence for a meeting
	// already warned about.
	lastWake int
	// lastMeeting is the minute-truncated StartsAt of the meeting lastWake
	// refers to.
	lastMeeting time.Time
	// live tracks whether the last poll produced a visible activity, so Run
	// knows when to call end().
	live bool
}

func NewMeetingProvider(clock Clock, src MeetingSource) *MeetingProvider {
	return &MeetingProvider{clock: clock, src: src, lastWake: -1}
}

func (m *MeetingProvider) Name() string { return "meeting" }

func (m *MeetingProvider) activityFor(n *NextMeeting) (Activity, bool) {
	if n == nil {
		return Activity{}, false
	}
	now := m.clock.Now()
	mins := int(n.StartsAt.Sub(now).Minutes())
	if mins < 0 || mins > LookaheadMinutes {
		return Activity{}, false
	}

	// A different StartsAt means a different meeting instance (e.g. the
	// previous meeting just ended and the source handed us the next one, or a
	// meeting was added/rescheduled ahead of what we were tracking). Give it
	// a fresh threshold sequence rather than inheriting stale bookkeeping.
	// Truncated to minute granularity: the countdown itself only has minute
	// resolution, so sub-minute jitter in StartsAt across polls (timezone
	// re-normalization, source precision differences) must not look like a
	// new meeting and replay the sequence.
	key := n.StartsAt.Truncate(time.Minute)
	if !key.Equal(m.lastMeeting) {
		m.lastWake = -1
		m.lastMeeting = key
	}

	significant := false
	for _, th := range wakeThresholds {
		if mins <= th {
			// Wake only on crossing into a NEARER threshold. This also handles a
			// poll that skips a minute (6 -> 4 never sees exactly 5): the first
			// poll at or under 5 still fires.
			if m.lastWake < 0 || th < m.lastWake {
				significant = true
				m.lastWake = th
			}
			break
		}
	}

	return Activity{
		ID:       "meeting.next",
		Kind:     "meeting",
		Priority: 70,
		Data: map[string]any{
			"title":   n.Title,
			"minutes": mins,
			"joinURL": n.JoinURL,
		},
		Started:     n.StartsAt,
		Ends:        n.StartsAt,
		Significant: significant,
	}, true
}

// poll fetches the next meeting and emits/ends as appropriate. Factored out
// of Run as a method (rather than a closure) so tests can drive Run's
// transition logic deterministically — repeated direct calls — without
// waiting on a real one-minute ticker.
func (m *MeetingProvider) poll(ctx context.Context, emit func(Activity), end func(string)) error {
	if m.src == nil {
		return nil
	}
	n, err := m.src(ctx)
	if err != nil {
		return err
	}
	a, ok := m.activityFor(n)
	if ok {
		emit(a)
		m.live = true
	} else if m.live {
		end("meeting.next")
		m.live = false
		// Deliberately NOT resetting lastWake/lastMeeting here. A transient
		// source blip (a single poll returning nil, e.g. a flaky calendar
		// sync) also takes this branch; if it cleared the bookkeeping, the
		// very next poll returning the SAME meeting would look like a brand
		// new instance and replay the entire T-5/T-1/T-0 sequence for a
		// meeting already warned about. Genuine replacement by a different
		// meeting is already handled by activityFor's own instance check
		// (StartsAt, minute-truncated), which does not depend on this branch
		// running first.
	}
	return nil
}

// Run polls once a minute.
func (m *MeetingProvider) Run(ctx context.Context, emit func(Activity), end func(string)) error {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	if err := m.poll(ctx, emit, end); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := m.poll(ctx, emit, end); err != nil {
				return err
			}
		}
	}
}
