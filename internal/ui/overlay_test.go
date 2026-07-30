package ui

import "testing"

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
