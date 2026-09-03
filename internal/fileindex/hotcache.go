// This file implements the Tier-1 hot cache (Task 5 of
// docs/superpowers/plans/2026-09-03-file-intelligence-index.md): an in-process
// LRU of recently opened paths plus an alias/explicit-memory → path map, rebuilt
// from SQLite on start and consulted before FTS. Lookups are existence-checked by
// the caller (Search/Resolve).
package fileindex

import "sync"

// defaultHotCap bounds the recent-paths LRU.
const defaultHotCap = 128

// hotCache is the Tier-1 in-RAM cache: explicit memory keys and heuristic aliases
// mapped to a path, plus a bounded most-recently-opened list.
type hotCache struct {
	mu        sync.RWMutex
	memory    map[string]string // explicit key -> path ("this is my resume")
	aliasPath map[string]string // heuristic alias -> path (most-recent wins)
	recent    []string          // paths, most-recent first
	cap       int
}

func newHotCache(cap int) *hotCache {
	if cap <= 0 {
		cap = defaultHotCap
	}
	return &hotCache{
		memory:    make(map[string]string),
		aliasPath: make(map[string]string),
		cap:       cap,
	}
}

// rebuild replaces the cache contents from SQLite-loaded maps (called on New).
// Keys are expected already normalized by the caller.
func (h *hotCache) rebuild(memory, aliasPath map[string]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.memory = make(map[string]string, len(memory))
	for k, v := range memory {
		h.memory[k] = v
	}
	h.aliasPath = make(map[string]string, len(aliasPath))
	for k, v := range aliasPath {
		h.aliasPath[k] = v
	}
}

// lookup returns the path for a normalized key: explicit memory wins over an
// alias. The caller must existence-check the result.
func (h *hotCache) lookup(key string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if p, ok := h.memory[key]; ok {
		return p, true
	}
	if p, ok := h.aliasPath[key]; ok {
		return p, true
	}
	return "", false
}

// recordOpen promotes path in the LRU and points its aliases at it (most-recent
// open wins ties for a shared alias like "resume").
func (h *hotCache) recordOpen(path string, aliases []string) {
	if path == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	// Move-to-front in the recent list.
	filtered := h.recent[:0]
	for _, p := range h.recent {
		if p != path {
			filtered = append(filtered, p)
		}
	}
	h.recent = append([]string{path}, filtered...)
	if len(h.recent) > h.cap {
		h.recent = h.recent[:h.cap]
	}

	for _, a := range aliases {
		if a != "" {
			h.aliasPath[a] = path
		}
	}
}

// remember pins an explicit normalized key to a path.
func (h *hotCache) remember(key, path string) {
	if key == "" || path == "" {
		return
	}
	h.mu.Lock()
	h.memory[key] = path
	h.mu.Unlock()
}
