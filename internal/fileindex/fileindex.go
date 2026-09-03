package fileindex

import (
	"context"
	"log"
	"strings"
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
}

// New opens (creating if needed) the index at dbPath over the given roots,
// honoring the exclude list. embedder may be nil.
func New(dbPath string, roots, exclude []string, embedder Embedder) (*Index, error) {
	store, err := OpenStore(dbPath)
	if err != nil {
		return nil, err
	}
	return &Index{
		store:    store,
		roots:    roots,
		exclude:  exclude,
		embedder: embedder,
	}, nil
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

// Search returns indexed entries matching query, narrowed by kind. This is a
// stub over store.SearchFTS for now; the full ranking pipeline lands in Task 4.
func (ix *Index) Search(query string, kind Kind) []FileRecord {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}

	files, err := ix.store.SearchFTS(query, 50)
	if err != nil {
		log.Printf("fileindex: search %q: %v", query, err)
		return nil
	}

	out := make([]FileRecord, 0, len(files))
	for _, f := range files {
		switch kind {
		case KindFile:
			if f.IsDir {
				continue
			}
		case KindFolder:
			if !f.IsDir {
				continue
			}
		}
		out = append(out, FileRecord{Path: f.Path, Name: f.Name})
	}
	return out
}
