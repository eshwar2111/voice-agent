package engine

import (
	"context"
	"testing"
	"time"

	"github.com/yourname/voice-agent/internal/ui"
)

func closed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// The double-fire mechanism: completion used to be signalled on one shared
// channel with no link to the trigger being acknowledged. A duplicate trigger
// the engine REJECTED still signalled done, which could release an unrelated
// capture that was still running — the wake-word loop then reclaimed the
// microphone mid-command and fired again.
func TestRejectedTriggerDoesNotReleaseTheInFlightOne(t *testing.T) {
	inflight := make(chan struct{})
	dup := make(chan struct{})

	e := &Engine{Events: make(chan Event, 10)}
	e.isBusy = true
	e.pending = ui.Trigger{Done: inflight}

	e.handleEvent(context.Background(), Event{
		Type:    EventVoiceInput,
		Payload: ui.Trigger{Done: dup},
	})

	if !closed(dup) {
		t.Error("a rejected trigger must release its own waiter immediately — it did no work")
	}
	if closed(inflight) {
		t.Fatal("a rejected duplicate released the in-flight command's waiter: double-fire is back")
	}
	if !e.IsBusy() {
		t.Error("rejecting a duplicate must not clear the busy flag")
	}
}

func TestCompletionReleasesTheTriggerThatStartedIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   Event
	}{
		{"success", Event{Type: EventToolExecuted}},
		{"failure", Event{Type: EventError, Err: context.DeadlineExceeded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			e := &Engine{Events: make(chan Event, 10)}
			e.isBusy = true
			e.pending = ui.Trigger{Done: done}

			e.handleEvent(context.Background(), tc.ev)

			if !closed(done) {
				t.Error("the trigger that started the command was never released")
			}
			if e.IsBusy() {
				t.Error("busy flag must clear when the command ends")
			}
		})
	}
}

// finishCommand clears e.pending as it releases it, so a second completion
// event (or a retry path that double-reports) cannot close the same channel
// twice and panic.
func TestFinishCommandIsIdempotent(t *testing.T) {
	done := make(chan struct{})
	e := &Engine{Events: make(chan Event, 10)}
	e.pending = ui.Trigger{Done: done}

	e.finishCommand()
	e.finishCommand() // must not panic on a second close

	if !closed(done) {
		t.Error("first finishCommand should have released the waiter")
	}
}

// A fire-and-forget trigger (the pill click) carries no channel at all. Every
// release path has to tolerate that.
func TestNilDoneTriggerIsSafe(t *testing.T) {
	e := &Engine{Events: make(chan Event, 10)}
	e.isBusy = true
	e.pending = ui.Trigger{}

	e.handleEvent(context.Background(), Event{Type: EventVoiceInput, Payload: ui.Trigger{}})
	e.handleEvent(context.Background(), Event{Type: EventToolExecuted})

	if e.IsBusy() {
		t.Error("busy flag should have cleared")
	}
}

// emit must not park forever once Start()'s loop has stopped draining Events.
func TestEmitGivesUpOnCancelledContext(t *testing.T) {
	e := &Engine{Events: make(chan Event)} // unbuffered: nobody is receiving
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	returned := make(chan struct{})
	go func() {
		e.emit(ctx, Event{Type: EventError})
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("emit blocked on a cancelled context — this is the shutdown goroutine leak")
	}
}
