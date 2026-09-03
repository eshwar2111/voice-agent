// Package fileindex implements a tiered, persistent file-intelligence index:
// an in-RAM alias/hot cache, a SQLite+FTS5 metadata store with ranked search,
// and a lazy local semantic fallback. This file implements the SQLite store
// (Task 1 of docs/superpowers/plans/2026-09-03-file-intelligence-index.md).
package fileindex

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// File is a single indexed filesystem entry (file or directory).
type File struct {
	ID           int64
	Path         string
	Name         string
	Ext          string
	Parent       string
	IsDir        bool
	Size         int64
	CreatedAt    int64
	ModifiedAt   int64
	LastAccessed int64
	ContentHash  string
	UsageScore   float64
}

// Store wraps the SQLite-backed metadata index.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS files (
  id            INTEGER PRIMARY KEY,
  path          TEXT UNIQUE NOT NULL,
  name          TEXT NOT NULL,
  ext           TEXT,
  parent        TEXT,
  is_dir        INTEGER NOT NULL DEFAULT 0,
  size          INTEGER,
  created_at    INTEGER,
  modified_at   INTEGER,
  last_accessed INTEGER,
  content_hash  TEXT,
  usage_score   REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_files_name ON files(name);
CREATE INDEX IF NOT EXISTS idx_files_hash ON files(content_hash);

CREATE VIRTUAL TABLE IF NOT EXISTS files_fts USING fts5(
  name, parent, keywords
);

CREATE TABLE IF NOT EXISTS aliases (
  file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  alias   TEXT NOT NULL,
  source  TEXT NOT NULL,
  UNIQUE(file_id, alias)
);
CREATE INDEX IF NOT EXISTS idx_aliases_alias ON aliases(alias);

CREATE TABLE IF NOT EXISTS file_usage (
  file_id     INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
  open_count  INTEGER NOT NULL DEFAULT 0,
  last_opened INTEGER
);

CREATE TABLE IF NOT EXISTS file_memory (
  key         TEXT PRIMARY KEY,
  path        TEXT NOT NULL,
  created_at  INTEGER
);

CREATE TABLE IF NOT EXISTS embeddings (
  content_hash TEXT PRIMARY KEY,
  dims         INTEGER NOT NULL,
  vec          BLOB NOT NULL,
  model        TEXT,
  created_at   INTEGER
);
`

// OpenStore opens (creating if needed) the SQLite index at path in WAL mode
// and runs migrations.
func OpenStore(path string) (*Store, error) {
	// _foreign_keys=on is REQUIRED for the ON DELETE CASCADE on aliases/file_usage
	// to fire — SQLite ignores FK constraints otherwise, orphaning usage rows whose
	// open_count then leaks onto a later file that reuses the deleted rowid, skewing
	// ranking. (mattn/go-sqlite3 honors this DSN param per-connection.)
	db, err := sql.Open("sqlite3", path+"?_journal=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("fileindex: open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("fileindex: migrate schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Upsert inserts or updates a file row keyed by path, syncing the files_fts
// row (keyed by the same rowid), and returns the row id.
func (s *Store) Upsert(f File) (int64, error) {
	isDir := 0
	if f.IsDir {
		isDir = 1
	}

	_, err := s.db.Exec(`
		INSERT INTO files (path, name, ext, parent, is_dir, size, created_at, modified_at, last_accessed, content_hash, usage_score)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name=excluded.name,
			ext=excluded.ext,
			parent=excluded.parent,
			is_dir=excluded.is_dir,
			size=excluded.size,
			created_at=excluded.created_at,
			modified_at=excluded.modified_at,
			last_accessed=excluded.last_accessed,
			content_hash=excluded.content_hash,
			usage_score=excluded.usage_score
	`, f.Path, f.Name, f.Ext, f.Parent, isDir, f.Size, f.CreatedAt, f.ModifiedAt, f.LastAccessed, f.ContentHash, f.UsageScore)
	if err != nil {
		return 0, fmt.Errorf("fileindex: upsert file: %w", err)
	}

	var id int64
	if err := s.db.QueryRow(`SELECT id FROM files WHERE path = ?`, f.Path).Scan(&id); err != nil {
		return 0, fmt.Errorf("fileindex: select id after upsert: %w", err)
	}

	// Sync the FTS row keyed by rowid=files.id. keywords is populated by
	// callers deriving aliases (Task 5); empty for now.
	if _, err := s.db.Exec(`DELETE FROM files_fts WHERE rowid = ?`, id); err != nil {
		return 0, fmt.Errorf("fileindex: clear fts row: %w", err)
	}
	if _, err := s.db.Exec(`INSERT INTO files_fts (rowid, name, parent, keywords) VALUES (?, ?, ?, '')`, id, f.Name, f.Parent); err != nil {
		return 0, fmt.Errorf("fileindex: insert fts row: %w", err)
	}

	return id, nil
}

// DeleteByPath removes the file row for path (cascading aliases/usage) and
// its files_fts row.
func (s *Store) DeleteByPath(path string) error {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM files WHERE path = ?`, path).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fileindex: lookup id for delete: %w", err)
	}

	if _, err := s.db.Exec(`DELETE FROM files_fts WHERE rowid = ?`, id); err != nil {
		return fmt.Errorf("fileindex: delete fts row: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM files WHERE id = ?`, id); err != nil {
		return fmt.Errorf("fileindex: delete file row: %w", err)
	}
	return nil
}

// GetByPath returns the file row for path, if present.
func (s *Store) GetByPath(path string) (*File, bool, error) {
	row := s.db.QueryRow(`
		SELECT id, path, name, ext, parent, is_dir, size, created_at, modified_at, last_accessed, content_hash, usage_score
		FROM files WHERE path = ?`, path)

	var f File
	var isDir int
	err := row.Scan(&f.ID, &f.Path, &f.Name, &f.Ext, &f.Parent, &isDir, &f.Size, &f.CreatedAt, &f.ModifiedAt, &f.LastAccessed, &f.ContentHash, &f.UsageScore)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("fileindex: get by path: %w", err)
	}
	f.IsDir = isDir != 0
	return &f, true, nil
}

// AllPaths returns a path->id map of every indexed file, for reconcile.
func (s *Store) AllPaths() (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT path, id FROM files`)
	if err != nil {
		return nil, fmt.Errorf("fileindex: all paths: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var p string
		var id int64
		if err := rows.Scan(&p, &id); err != nil {
			return nil, fmt.Errorf("fileindex: scan path: %w", err)
		}
		out[p] = id
	}
	return out, rows.Err()
}

// SearchFTS runs a full-text query over name/parent/keywords, falling back
// to a LIKE scan on name if FTS5 is unavailable or errors.
func (s *Store) SearchFTS(query string, limit int) ([]File, error) {
	ftsQuery := ftsPrefixQuery(query)

	rows, err := s.db.Query(`
		SELECT f.id, f.path, f.name, f.ext, f.parent, f.is_dir, f.size, f.created_at, f.modified_at, f.last_accessed, f.content_hash, f.usage_score
		FROM files_fts
		JOIN files f ON f.id = files_fts.rowid
		WHERE files_fts MATCH ?
		LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return s.searchLike(query, limit)
	}
	defer rows.Close()

	var out []File
	for rows.Next() {
		f, serr := scanFile(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return s.searchLike(query, limit)
	}
	// Even when FTS ran cleanly, a zero result can be a tokenization miss
	// (e.g. "startup brainstorm" vs the single FTS token "startup_brainstorm").
	// Fall through to the normalized token LIKE so the query still resolves.
	if len(out) == 0 {
		return s.searchLike(query, limit)
	}
	return out, nil
}

// searchLike matches every query token against the file name with separators
// normalized to spaces (so "startup brainstorm" matches "startup_brainstorm.txt",
// "my-notes", "report.final", etc.). All tokens must be present (AND).
func (s *Store) searchLike(query string, limit int) ([]File, error) {
	toks := strings.Fields(strings.ToLower(query))
	if len(toks) == 0 {
		return nil, nil
	}
	const norm = `lower(replace(replace(replace(name,'_',' '),'-',' '),'.',' '))`
	conds := make([]string, 0, len(toks))
	args := make([]any, 0, len(toks)+1)
	for _, t := range toks {
		conds = append(conds, norm+" LIKE ?")
		args = append(args, "%"+t+"%")
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT id, path, name, ext, parent, is_dir, size, created_at, modified_at, last_accessed, content_hash, usage_score
		FROM files WHERE `+strings.Join(conds, " AND ")+` LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("fileindex: like fallback: %w", err)
	}
	defer rows.Close()

	var out []File
	for rows.Next() {
		f, serr := scanFile(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// rowScanner is satisfied by *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanFile(rows rowScanner) (File, error) {
	var f File
	var isDir int
	if err := rows.Scan(&f.ID, &f.Path, &f.Name, &f.Ext, &f.Parent, &isDir, &f.Size, &f.CreatedAt, &f.ModifiedAt, &f.LastAccessed, &f.ContentHash, &f.UsageScore); err != nil {
		return File{}, fmt.Errorf("fileindex: scan file: %w", err)
	}
	f.IsDir = isDir != 0
	return f, nil
}

// SetAliases replaces the heuristic aliases for fileID and updates the FTS
// keywords column (space-joined aliases). User-pinned aliases (source!='heuristic')
// are left untouched.
func (s *Store) SetAliases(fileID int64, aliases []string) error {
	if _, err := s.db.Exec(`DELETE FROM aliases WHERE file_id = ? AND source = 'heuristic'`, fileID); err != nil {
		return fmt.Errorf("fileindex: clear aliases: %w", err)
	}
	for _, a := range aliases {
		if a == "" {
			continue
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO aliases (file_id, alias, source) VALUES (?, ?, 'heuristic')`, fileID, a); err != nil {
			return fmt.Errorf("fileindex: insert alias: %w", err)
		}
	}
	if _, err := s.db.Exec(`UPDATE files_fts SET keywords = ? WHERE rowid = ?`, strings.Join(aliases, " "), fileID); err != nil {
		return fmt.Errorf("fileindex: update fts keywords: %w", err)
	}
	return nil
}

// FileIDsForAlias returns the set of file ids that carry the given alias.
func (s *Store) FileIDsForAlias(alias string) (map[int64]bool, error) {
	rows, err := s.db.Query(`SELECT file_id FROM aliases WHERE alias = ?`, alias)
	if err != nil {
		return nil, fmt.Errorf("fileindex: file ids for alias: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("fileindex: scan alias file id: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// UniqueAliasPaths returns alias->path for aliases that resolve to exactly one
// file, for rebuilding the hot cache unambiguously on start.
func (s *Store) UniqueAliasPaths() (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT a.alias, MIN(f.path)
		FROM aliases a JOIN files f ON f.id = a.file_id
		GROUP BY a.alias
		HAVING COUNT(*) = 1`)
	if err != nil {
		return nil, fmt.Errorf("fileindex: unique alias paths: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var alias, path string
		if err := rows.Scan(&alias, &path); err != nil {
			return nil, fmt.Errorf("fileindex: scan alias path: %w", err)
		}
		out[alias] = path
	}
	return out, rows.Err()
}

// RecordUsage bumps open_count/last_opened for path and recomputes the file's
// usage_score (currently the open count; usageNorm caps its influence). A path
// not in the index is a no-op.
func (s *Store) RecordUsage(path string, now int64) error {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM files WHERE path = ?`, path).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fileindex: lookup id for usage: %w", err)
	}

	if _, err := s.db.Exec(`
		INSERT INTO file_usage (file_id, open_count, last_opened)
		VALUES (?, 1, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			open_count = open_count + 1,
			last_opened = excluded.last_opened`, id, now); err != nil {
		return fmt.Errorf("fileindex: bump usage: %w", err)
	}

	var count int64
	if err := s.db.QueryRow(`SELECT open_count FROM file_usage WHERE file_id = ?`, id).Scan(&count); err != nil {
		return fmt.Errorf("fileindex: read usage count: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE files SET usage_score = ?, last_accessed = ? WHERE id = ?`, float64(count), now, id); err != nil {
		return fmt.Errorf("fileindex: update usage score: %w", err)
	}
	return nil
}

// SetMemory upserts an explicit key->path memory ("this is my latest resume").
func (s *Store) SetMemory(key, path string, now int64) error {
	if _, err := s.db.Exec(`
		INSERT INTO file_memory (key, path, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			path = excluded.path,
			created_at = excluded.created_at`, key, path, now); err != nil {
		return fmt.Errorf("fileindex: set memory: %w", err)
	}
	return nil
}

// AllMemory returns every explicit key->path memory, for rebuilding the hot cache.
func (s *Store) AllMemory() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, path FROM file_memory`)
	if err != nil {
		return nil, fmt.Errorf("fileindex: all memory: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var key, path string
		if err := rows.Scan(&key, &path); err != nil {
			return nil, fmt.Errorf("fileindex: scan memory: %w", err)
		}
		out[key] = path
	}
	return out, rows.Err()
}

// ftsPrefixQuery turns free text into an FTS5 MATCH query of prefix tokens,
// e.g. "resume 2026" -> `"resume"* "2026"*`.
func ftsPrefixQuery(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return `""`
	}
	tokens := make([]string, 0, len(fields))
	for _, tok := range fields {
		tok = strings.ReplaceAll(tok, `"`, "")
		if tok == "" {
			continue
		}
		tokens = append(tokens, fmt.Sprintf(`"%s"*`, tok))
	}
	if len(tokens) == 0 {
		return `""`
	}
	return strings.Join(tokens, " ")
}
