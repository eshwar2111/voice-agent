package ui

import (
	"sync"
	"time"
)

// Integration status now VALIDATES the stored token by exchanging it, because
// a stored token is not a working one. That turns a cheap local read into real
// network I/O — and the Control Center polls status every 4 seconds while it
// is open, which would mean a Google token exchange and a Spotify /me call
// every 4 seconds, forever, per open panel.
//
// The cache bounds that at the source rather than trusting every caller to
// poll politely. A stale-by-seconds badge is invisible to the user; a request
// storm is not.
const authStatusTTL = 20 * time.Second

type cachedStatus struct {
	val map[string]interface{}
	at  time.Time
}

var (
	authStatusMu sync.Mutex
	authStatuses = map[string]cachedStatus{}
)

// cachedAuthStatus returns a recent status for provider, computing it with
// fetch only when the cached copy has aged out.
func cachedAuthStatus(provider string, fetch func() map[string]interface{}) map[string]interface{} {
	authStatusMu.Lock()
	if c, ok := authStatuses[provider]; ok && time.Since(c.at) < authStatusTTL {
		v := c.val
		authStatusMu.Unlock()
		return v
	}
	authStatusMu.Unlock()

	// Compute OUTSIDE the lock: fetch performs network I/O, and holding a
	// global mutex across it would serialise every provider's status behind
	// the slowest one.
	v := fetch()

	authStatusMu.Lock()
	authStatuses[provider] = cachedStatus{val: v, at: time.Now()}
	authStatusMu.Unlock()
	return v
}

// invalidateAuthStatus drops a provider's cached status so the next read is
// fresh. Called after linking or unlinking, where waiting out the TTL would
// leave the UI showing the state the user just changed.
func invalidateAuthStatus(provider string) {
	authStatusMu.Lock()
	delete(authStatuses, provider)
	authStatusMu.Unlock()
}
