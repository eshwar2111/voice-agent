package ui

import (
	"sync"
	"time"
)

// progress.go — the task progress card (interaction model: a long/multi-step
// task shows a compact progress card with a Stop, and can pause for a mid-task
// decision via AskChoice). Driven over the existing activity channel; the island
// renders the "task.progress" activity kind. A long task calls StartProgress,
// then Update(...) as it goes, then Done/Fail.

const progressActivityID = "task.progress"

var (
	progressMu     sync.Mutex
	progressCancel func()
)

// ProgressHandle drives one task's progress card.
type ProgressHandle struct {
	title      string
	cancelable bool
}

// StartProgress shows a progress card titled title. cancel is invoked when the
// user presses Stop (nil => no Stop button). A no-op safe handle is returned
// even when the UI isn't up, so callers never need to nil-check.
func StartProgress(title string, cancel func()) *ProgressHandle {
	progressMu.Lock()
	progressCancel = cancel
	progressMu.Unlock()
	h := &ProgressHandle{title: title, cancelable: cancel != nil}
	h.push("running", 0, 0, "")
	return h
}

func (h *ProgressHandle) push(phase string, done, total int, note string) {
	if h == nil {
		return
	}
	UpdateActivity(progressActivityID, map[string]any{
		"kind":       progressActivityID,
		"title":      h.title,
		"phase":      phase, // running | done | error
		"done":       done,
		"total":      total,
		"note":       note,
		"cancelable": h.cancelable,
	})
}

// Update reports progress: done of total (total <= 0 = indeterminate) with an
// optional human note (e.g. "Reading 3 of 8 sources").
func (h *ProgressHandle) Update(done, total int, note string) { h.push("running", done, total, note) }

// Note updates just the status line (indeterminate progress).
func (h *ProgressHandle) Note(note string) { h.push("running", 0, 0, note) }

// Done marks the task finished — a brief ✓ card, then it dismisses. steps, when
// non-empty, renders the Action Trail (each a completed step label).
func (h *ProgressHandle) Done(summary string, steps []string) {
	if h == nil {
		return
	}
	if len(steps) > 0 {
		UpdateActivity(progressActivityID, map[string]any{
			"kind": progressActivityID, "title": h.title, "phase": "done",
			"done": 0, "total": 0, "note": summary, "cancelable": false, "steps": steps,
		})
	} else {
		h.push("done", 0, 0, summary)
	}
	clearProgressCancel()
	go func() {
		time.Sleep(2500 * time.Millisecond)
		EndActivity(progressActivityID)
	}()
}

// Fail marks the task failed.
func (h *ProgressHandle) Fail(msg string) {
	if h == nil {
		return
	}
	h.push("error", 0, 0, msg)
	clearProgressCancel()
	go func() {
		time.Sleep(3 * time.Second)
		EndActivity(progressActivityID)
	}()
}

func clearProgressCancel() {
	progressMu.Lock()
	progressCancel = nil
	progressMu.Unlock()
}

// MediaControlFunc, when set, runs a media transport action ("previous" /
// "pause" (toggle) / "next") — wired in main to the media_control tool. The
// media widget's transport buttons call it via the spotify* bindings.
var MediaControlFunc func(action string)

func mediaControl(action string) {
	if MediaControlFunc != nil {
		MediaControlFunc(action)
	}
}

// invokeProgressCancel is called by the island's Stop button (taskStop binding).
func invokeProgressCancel() {
	progressMu.Lock()
	c := progressCancel
	progressMu.Unlock()
	if c != nil {
		c()
	}
}
