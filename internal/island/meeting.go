package island

import (
	"context"
	"time"
)

// LookaheadMinutes bounds how early a meeting appears. The island shows what is
// imminent; it is not a calendar.
const LookaheadMinutes = 60

// wakeThresholds are the remaining-minute values worth interrupting for.
var wakeThresholds = []int{5, 1, 0}

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
	// re-wake the island every minute at the same threshold.
	lastWake int
	started  time.Time
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

	significant := false
	for _, th := range wakeThresholds {
		if mins == th && m.lastWake != th {
			significant = true
			m.lastWake = th
			break
		}
	}

	// Started identifies the instance for dismissal; use the meeting's own
	// start so it stays stable across polls.
	if m.started.IsZero() {
		m.started = n.StartsAt
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

// Run polls once a minute.
func (m *MeetingProvider) Run(ctx context.Context, emit func(Activity), end func(string)) error {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	live := false
	poll := func() error {
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
			live = true
		} else if live {
			end("meeting.next")
			live = false
			m.lastWake = -1
		}
		return nil
	}
	if err := poll(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := poll(); err != nil {
				return err
			}
		}
	}
}
