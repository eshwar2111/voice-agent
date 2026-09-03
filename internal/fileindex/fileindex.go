package fileindex

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Embedder produces vector embeddings for a batch of texts. It is injected from
// main (implemented by the ONNX BGE embedder in Task 7); nil is allowed, in
// which case the semantic tier is disabled and Tiers 1-2 still work.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Kind narrows a search/resolve to files, folders, or either.
type Kind int

const (
	KindAny Kind = iota
	KindFile
	KindFolder
)

// FileRecord mirrors search.FileRecord so callers can be migrated behind a shim.
type FileRecord struct {
	Path string
	Name string
}

// Index is the top-level file-intelligence index: a SQLite-backed metadata
// store scanned/watched over a set of roots, with an optional semantic embedder.
type Index struct {
	store    *Store
	roots    []string
	exclude  []string
	embedder Embedder // nil ok
	hot      *hotCache
}

// New opens (creating if needed) the index at dbPath over the given roots,
// honoring the exclude list. embedder may be nil.
func New(dbPath string, roots, exclude []string, embedder Embedder) (*Index, error) {
	store, err := OpenStore(dbPath)
	if err != nil {
		return nil, err
	}
	ix := &Index{
		store:    store,
		roots:    roots,
		exclude:  exclude,
		embedder: embedder,
		hot:      newHotCache(defaultHotCap),
	}
	ix.rebuildHotCache()
	return ix, nil
}

// rebuildHotCache repopulates the Tier-1 cache from SQLite (explicit memory +
// unambiguous aliases). Errors are logged and non-fatal — the cache is a
// best-effort accelerator over Tiers 2-3.
func (ix *Index) rebuildHotCache() {
	if ix.hot == nil || ix.store == nil {
		return
	}
	memory, err := ix.store.AllMemory()
	if err != nil {
		log.Printf("fileindex: hotcache memory load: %v", err)
		memory = nil
	}
	aliasPath, err := ix.store.UniqueAliasPaths()
	if err != nil {
		log.Printf("fileindex: hotcache alias load: %v", err)
		aliasPath = nil
	}
	ix.hot.rebuild(memory, aliasPath)
}

// Start kicks off the initial scan in a background goroutine, then launches the
// live fsnotify watcher for incremental updates.
func (ix *Index) Start(ctx context.Context) {
	go func() {
		ix.scan()
		ix.watch(ctx)
	}()
}

// Close releases the underlying store.
func (ix *Index) Close() error {
	if ix.store == nil {
		return nil
	}
	return ix.store.Close()
}

// nowUnix returns the current time as a Unix timestamp; a var so tests could
// stub it if ever needed.
var nowUnix = func() int64 { return time.Now().Unix() }

// upsertFile writes f to the store and (re)derives its heuristic aliases +
// FTS keywords. Used by the scan and watcher so every indexed entry is
// alias-searchable. Directories are aliased too (helps folder resolution).
func (ix *Index) upsertFile(f File) (int64, error) {
	id, err := ix.store.Upsert(f)
	if err != nil {
		return 0, err
	}
	if aerr := ix.store.SetAliases(id, deriveAliases(f.Name)); aerr != nil {
		log.Printf("fileindex: set aliases %s: %v", f.Path, aerr)
	}
	return id, nil
}

// RecordOpen records that path was opened: it bumps usage (open_count/score),
// sets last_opened, and refreshes the hot cache so the file resolves faster next
// time. A path outside the index is a no-op.
func (ix *Index) RecordOpen(path string) {
	if ix == nil || ix.store == nil || path == "" {
		return
	}
	if err := ix.store.RecordUsage(path, nowUnix()); err != nil {
		log.Printf("fileindex: record open %s: %v", path, err)
	}
	if f, ok, _ := ix.store.GetByPath(path); ok && f != nil {
		ix.hot.recordOpen(path, deriveAliases(f.Name))
	} else {
		ix.hot.recordOpen(path, nil)
	}
}

// Remember pins an explicit alias ("this is my latest resume") to a path so the
// query resolves instantly via the hot cache thereafter.
func (ix *Index) Remember(key, path string) error {
	if ix == nil || ix.store == nil {
		return fmt.Errorf("fileindex: index not ready")
	}
	k := normalizeKey(key)
	if k == "" || strings.TrimSpace(path) == "" {
		return fmt.Errorf("fileindex: remember needs a key and a path")
	}
	if err := ix.store.SetMemory(k, path, nowUnix()); err != nil {
		return err
	}
	ix.hot.remember(k, path)
	return nil
}

// aliasMatches returns the set of file ids whose stored aliases match the query
// (the whole normalized query and each of its tokens), for setting AliasMatch in
// ranking.
func (ix *Index) aliasMatches(query string) map[int64]bool {
	out := make(map[int64]bool)
	if ix.store == nil {
		return out
	}
	tried := make(map[string]bool)
	add := func(term string) {
		if term == "" || tried[term] {
			return
		}
		tried[term] = true
		ids, err := ix.store.FileIDsForAlias(term)
		if err != nil {
			return
		}
		for id := range ids {
			out[id] = true
		}
	}
	norm := normalizeKey(query)
	add(norm)
	for _, tok := range strings.Fields(norm) {
		add(tok)
	}
	return out
}
