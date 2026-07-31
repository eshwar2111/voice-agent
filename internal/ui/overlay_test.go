package ui

import (
	"testing"
	"time"
)

// Regression for a fix-round-2 finding: RequestConfirmation(Card) guarded only
// on w != nil, so the window between w and bridge being assigned in
// StartOverlay let a confirmation prompt push into a nil Bridge (silently, by
// design — see bridge.go's nil guard) and then block forever on
// <-confirmChan, since no prompt was ever shown for the caller to answer.
// canDeliverConfirmation is the pure guard both RequestConfirmation and
// RequestConfirmationCard must pass before pushing and reading confirmChan.
func TestCanDeliverConfirmation(t *testing.T) {
	cases := []struct {
		name            string
		hasWindow       bool
		hasBridge       bool
		wantDeliverable bool
	}{
		{"no window, no bridge", false, false, false},
		{"window but no bridge (the startup race window)", true, false, false},
		{"bridge but no window (shouldn't happen, still must deny)", false, true, false},
		{"window and bridge ready", true, true, true},
	}

	for _, tc := range cases {
		got := canDeliverConfirmation(tc.hasWindow, tc.hasBridge)
		if got != tc.wantDeliverable {
			t.Errorf("%s: canDeliverConfirmation(%v, %v) = %v, want %v",
				tc.name, tc.hasWindow, tc.hasBridge, got, tc.wantDeliverable)
		}
	}
}

// Regression for a fix-round-2 finding: RequestConfirmation and
// RequestConfirmationCard share one confirmChan and one 'trust.approval'
// activity slot. The typed `ai …` path isn't covered by the engine's isBusy
// guard, so it can overlap the voice path — before this fix, two goroutines
// could both reach UpdateActivity, with the second silently clobbering the
// first's prompt, leaving one of them blocked on <-confirmChan forever with
// no UI and no log to explain why.
//
// RequestConfirmation(Card) themselves can't be exercised here without a
// real WebView (canDeliverConfirmation denies with w == nil, as in every
// other test in this package), so this drives waitForConfirmSlot directly —
// the same serialization primitive both functions call before ever touching
// confirmChan or UpdateActivity.
func TestWaitForConfirmSlotSerializesConcurrentCallers(t *testing.T) {
	waitForConfirmSlot() // first caller acquires immediately (uncontended)

	started := make(chan struct{})
	acquired := make(chan struct{})
	go func() {
		close(started)
		waitForConfirmSlot() // must block until the first caller unlocks
		close(acquired)
	}()
	<-started

	select {
	case <-acquired:
		t.Fatal("second waitForConfirmSlot returned before the first caller released it — approvals are not serialized")
	case <-time.After(50 * time.Millisecond):
		// Expected: still blocked.
	}

	confirmMutex.Unlock() // release the first "pending approval"

	select {
	case <-acquired:
		// Expected: the second caller now holds the slot.
	case <-time.After(time.Second):
		t.Fatal("second waitForConfirmSlot never acquired the slot after the first was released")
	}
	confirmMutex.Unlock() // release the second holder for test cleanliness
}
