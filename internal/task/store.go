package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store persists sessions as JSON under Dir so a task survives across exchanges
// and app restarts. Because steps are data (not closures), a persisted session
// fully reconstructs and resumes from its Cursor. Terminal sessions are removed.
type Store struct {
	Dir string
	mu  sync.Mutex
}

func NewStore(dir string) *Store { return &Store{Dir: dir} }

func (st *Store) path(id string) string { return filepath.Join(st.Dir, id+".json") }

// Save writes a running/waiting session; a terminal one is deleted (finished
// tasks aren't kept). Wire this as Deps.Save so the runner persists each step.
func (st *Store) Save(s *Session) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if s.Done() {
		_ = os.Remove(st.path(s.ID))
		return
	}
	if err := os.MkdirAll(st.Dir, 0o755); err != nil {
		return
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(st.path(s.ID), b, 0o644)
}

// LoadPending returns every non-terminal session on disk (for resume on
// startup), oldest first is not guaranteed — order is filesystem order.
func (st *Store) LoadPending() []*Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	entries, err := os.ReadDir(st.Dir)
	if err != nil {
		return nil
	}
	var out []*Session
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(st.Dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		if s.Done() {
			continue
		}
		sCopy := s
		out = append(out, &sCopy)
	}
	return out
}
