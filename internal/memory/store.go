package memory

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"
)

// newID generates a random hex ID for a memory record.
func newID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return fmt.Sprintf("%x-%d", b, time.Now().UnixNano())
}

// Store handles reading and writing memories to SQLite.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store and ensures the schema is up to date.
func NewStore(db *sql.DB) (*Store, error) {
	if err := InitSchema(db); err != nil {
		return nil, fmt.Errorf("memory: schema init failed: %w", err)
	}
	return &Store{db: db}, nil
}

// Save inserts a new memory. Returns the generated ID.
// Deduplication: if a memory with very similar content already exists, it is updated
// in place (importance bumped) instead of creating a duplicate.
func (s *Store) Save(memType MemoryType, content string, importance int) (string, error) {
	// Deduplicate: check if a similar memory already exists.
	// We use a LIKE match on the first 60 chars of the content (or the full string if shorter).
	excerpt := content
	if len(excerpt) > 60 {
		excerpt = excerpt[:60]
	}
	var existingID string
	err := s.db.QueryRow(
		`SELECT id FROM memories WHERE content LIKE ? LIMIT 1`,
		"%"+excerpt+"%",
	).Scan(&existingID)
	if err == nil && existingID != "" {
		// Duplicate found — bump importance if higher and update timestamp
		_, _ = s.db.Exec(
			`UPDATE memories SET importance = MAX(importance, ?), timestamp = ? WHERE id = ?`,
			importance, time.Now().UTC(), existingID,
		)
		return existingID, nil // return existing ID, no new row
	}

	id := newID()
	_, err = s.db.Exec(
		`INSERT INTO memories (id, type, content, importance, timestamp) VALUES (?, ?, ?, ?, ?)`,
		id, string(memType), content, importance, time.Now().UTC(),
	)
	if err != nil {
		return "", fmt.Errorf("memory: save failed: %w", err)
	}
	return id, nil
}

// Delete removes a memory by ID.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	return err
}

// PruneOld removes memories older than maxAge that have low importance.
func (s *Store) PruneOld(maxAge time.Duration, belowImportance int) (int64, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	res, err := s.db.Exec(
		`DELETE FROM memories WHERE timestamp < ? AND importance < ?`,
		cutoff, belowImportance,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Count returns the total number of stored memories.
func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n)
	return n, err
}

// List returns recent memories ordered by timestamp desc.
func (s *Store) List(limit int) ([]Memory, error) {
	rows, err := s.db.Query(
		`SELECT id, type, content, importance, timestamp FROM memories ORDER BY timestamp DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// --- Legacy KV methods for backward compatibility with SaveMemoryTool ---

// KVSave stores a key-value pair in the old memory table.
func (s *Store) KVSave(key, value string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO memory (key, value, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)`,
		key, value,
	)
	return err
}

// KVGet retrieves a value by key from the old memory table.
func (s *Store) KVGet(key string) (string, error) {
	var value string
	err := s.db.QueryRow(
		`SELECT value FROM memory WHERE key = ? ORDER BY created_at DESC LIMIT 1`,
		key,
	).Scan(&value)
	return value, err
}

// --- helpers ---

func scanMemories(rows *sql.Rows) ([]Memory, error) {
	var out []Memory
	for rows.Next() {
		var m Memory
		var ts string
		var memType string
		if err := rows.Scan(&m.ID, &memType, &m.Content, &m.Importance, &ts); err != nil {
			return nil, err
		}
		m.Type = MemoryType(memType)
		m.Timestamp, _ = time.Parse("2006-01-02T15:04:05Z", ts)
		if m.Timestamp.IsZero() {
			m.Timestamp, _ = time.Parse("2006-01-02 15:04:05", ts)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
