package fileindex

import (
	"context"
	"fmt"
	"log"
	"sort"
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
	// embedSem throttles background lazy-embed goroutines to one at a time so a
	// burst of opens can't queue many CPU-heavy ONNX runs.
	embedSem chan struct{}
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
		embedSem: make(chan struct{}, 1),
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
	// A file the user actually opened is a prime candidate for the semantic
	// cache; embed it lazily in the background (throttled, best-effort).
	ix.lazyEmbed(path)
}

// lazyEmbed embeds path's content in the background if an embedder is present
// and the throttle slot is free. It's a no-op when semantic is disabled or a
// background embed is already running.
func (ix *Index) lazyEmbed(path string) {
	if ix == nil || ix.embedder == nil || ix.embedSem == nil {
		return
	}
	select {
	case ix.embedSem <- struct{}{}:
	default:
		return // an embed is already running; skip (it'll be picked up later)
	}
	go func() {
		defer func() { <-ix.embedSem }()
		if _, err := ix.ensureVector(context.Background(), path); err != nil {
			log.Printf("fileindex: lazy embed %s: %v", path, err)
		}
	}()
}

// ensureVector returns the cached embedding for path's content, computing and
// caching it (keyed by content hash, deduped across copies) on a miss. It also
// records the content hash on the file row. Returns ("", nil) semantics via the
// hash: an empty hash means the file wasn't embeddable / couldn't be read.
func (ix *Index) ensureVector(ctx context.Context, path string) (string, error) {
	if ix.embedder == nil {
		return "", nil
	}
	text, ok := extractText(path)
	if !ok || strings.TrimSpace(text) == "" {
		return "", nil
	}
	hash := contentHash(text)
	_ = ix.store.SetContentHash(path, hash)

	if _, ok, _ := ix.store.GetVector(hash); ok {
		return hash, nil // already cached (possibly from a copy)
	}
	vecs, err := ix.embedder.Embed(ctx, []string{text})
	if err != nil {
		return "", err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return "", nil
	}
	if err := ix.store.PutVector(hash, len(vecs[0]), vecs[0], "bge-small-en-v1.5"); err != nil {
		return "", err
	}
	return hash, nil
}

// maxSemanticCandidates bounds how many likely files the semantic tier will
// consider (and lazily embed) on a single metadata miss — never a whole-PC pass.
const maxSemanticCandidates = 64

// semanticThreshold is the minimum cosine similarity for a semantic hit to be
// returned, keeping unrelated content out of the results.
const semanticThreshold = 0.55

// semanticSearch is the Tier-3 fallback: embed the query, ensure a bounded set
// of likely candidates have cached vectors, then brute-force cosine-rank. It is
// only called when Tiers 1-2 found nothing confident and an embedder is present.
func (ix *Index) semanticSearch(query string, kind Kind) []FileRecord {
	if ix.embedder == nil {
		return nil
	}
	ctx := context.Background()

	qvecs, err := ix.embedder.Embed(ctx, []string{bgeQueryPrefix + query})
	if err != nil || len(qvecs) == 0 || len(qvecs[0]) == 0 {
		if err != nil {
			log.Printf("fileindex: semantic query embed: %v", err)
		}
		return nil
	}
	qv := qvecs[0]

	cands, err := ix.store.RecentFiles(maxSemanticCandidates)
	if err != nil {
		log.Printf("fileindex: semantic candidates: %v", err)
		return nil
	}

	cached, err := ix.store.AllVectors()
	if err != nil {
		cached = map[string][]float32{}
	}

	type scored struct {
		path  string
		name  string
		score float64
	}
	var hits []scored
	for _, f := range cands {
		if kind == KindFolder || f.IsDir {
			continue // semantic is over file content only
		}
		if !isEmbeddable(f.Ext) {
			continue
		}
		text, ok := extractText(f.Path)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		hash := contentHash(text)
		vec, ok := cached[hash]
		if !ok {
			// Lazily embed this candidate now (bounded by maxSemanticCandidates).
			h, eerr := ix.ensureVector(ctx, f.Path)
			if eerr != nil || h == "" {
				continue
			}
			if v, ok2, _ := ix.store.GetVector(h); ok2 {
				vec = v
				cached[h] = v
			} else {
				continue
			}
		}
		hits = append(hits, scored{path: f.Path, name: f.Name, score: cosine(qv, vec)})
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })

	out := make([]FileRecord, 0, len(hits))
	for _, h := range hits {
		if h.score < semanticThreshold {
			break
		}
		out = append(out, FileRecord{Path: h.path, Name: h.name})
	}
	return out
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
