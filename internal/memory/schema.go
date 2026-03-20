package memory

import "database/sql"

// InitSchema creates all necessary tables for the memory system.
// Uses a standard indexed TEXT column for full-text search via LIKE queries
// (no FTS5 needed — works with the default mattn/go-sqlite3 build).
func InitSchema(db *sql.DB) error {
	stmts := []string{
		// Main memories table
		`CREATE TABLE IF NOT EXISTS memories (
			id         TEXT PRIMARY KEY,
			type       TEXT NOT NULL,
			content    TEXT NOT NULL,
			importance INTEGER NOT NULL DEFAULT 5,
			timestamp  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,

		// Index on content for faster LIKE searches
		`CREATE INDEX IF NOT EXISTS idx_memories_content ON memories(content)`,

		// Index on type for filtered queries
		`CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type)`,

		// Index on timestamp for TTL cleanup and listing
		`CREATE INDEX IF NOT EXISTS idx_memories_timestamp ON memories(timestamp)`,

		// Old KV memory table — kept for backward compatibility
		`CREATE TABLE IF NOT EXISTS memory (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT UNIQUE NOT NULL,
			value TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
