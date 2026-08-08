package ui

import (
	"sync"

	"github.com/yourname/voice-agent/internal/island"
)

// islandRegistry is the live island.Registry instance, set by main.go once it
// constructs one (Task 10). It is typed as a narrow interface — rather than
// the concrete *island.Registry — so internal/ui depends only on the single
// method it needs, not on how the registry is constructed or what else it
// can do. nil until main.go calls SetIslandRegistry (e.g. before
// TrustedExecution is configured, or in tests), so the dismiss binding below
// must treat that as a no-op, not a panic.
var islandRegistry interface{ Dismiss(string) }

// SetIslandRegistry wires the registry constructed in main.go into the
// dismiss binding below.
func SetIslandRegistry(r interface{ Dismiss(string) }) { islandRegistry = r }

// UpdateActivity pushes or refreshes a live activity in the island.
// Unknown ids are dropped by the JS registry with a log line, never thrown.
func UpdateActivity(id string, data any) {
	if bridge == nil {
		return
	}
	bridge.Push("activity:update", map[string]any{"id": id, "data": data})
}

// EndActivity removes a live activity.
func EndActivity(id string) {
	if bridge == nil {
		return
	}
	bridge.Push("activity:end", map[string]string{"id": id})
}

// pendingMu guards pendingActivities/havePending below. PublishActivities can
// be called from a provider goroutine (island.Runner) before StartOverlay has
// assigned bridge — main.go starts islandRunner before calling ui.StartOverlay,
// since StartOverlay blocks running the WebView loop for the life of the app.
// Bridge itself buffers pushes made after it exists but before JS signals
// uiReady; this only covers the earlier gap, where bridge is still nil and
// Push would never even be called.
var (
	pendingMu         sync.Mutex
	pendingActivities []map[string]any
	havePending       bool
)

// PublishActivities replaces the set of provider-driven activities in the
// island. It is the Publish func injected into island.Registry.
//
// This does NOT disturb push-driven activities (agent.job, trust.approval,
// ambient.nudge) — the JS side keeps the two sets separate and merges them for
// display, so a provider snapshot can never clear a pending approval.
//
// If called before bridge exists, the snapshot is buffered (not dropped) and
// flushed once by FlushPendingActivities. Only the latest snapshot is kept:
// each call is a full replacement of the provider-driven set, never a delta,
// so there is nothing to queue — an older buffered snapshot would just be
// overwritten by the next one anyway.
func PublishActivities(as []island.Activity) {
	out := make([]map[string]any, 0, len(as))
	for _, a := range as {
		out = append(out, map[string]any{
			"id":          a.ID,
			"kind":        a.Kind,
			"priority":    a.Priority,
			"data":        a.Data,
			"significant": a.Significant,
		})
	}

	if bridge == nil {
		pendingMu.Lock()
		pendingActivities = out
		havePending = true
		pendingMu.Unlock()
		return
	}
	bridge.Push("activity:sync", map[string]any{"activities": out})
}

// FlushPendingActivities publishes the most recent activity snapshot buffered
// while bridge was still nil (see PublishActivities), then clears it. Called
// once, from the uiReady binding in overlay.go, right after bridge.Ready() —
// by then bridge is guaranteed non-nil (StartOverlay assigns it before
// binding uiReady), so the flushed snapshot goes through Bridge's own
// buffer-until-ready path like any other push, rather than being evaluated
// immediately ahead of earlier-queued messages.
func FlushPendingActivities() {
	pendingMu.Lock()
	if !havePending {
		pendingMu.Unlock()
		return
	}
	out := pendingActivities
	pendingActivities = nil
	havePending = false
	pendingMu.Unlock()

	if bridge == nil {
		return
	}
	bridge.Push("activity:sync", map[string]any{"activities": out})
}

