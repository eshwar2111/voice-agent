package search

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yourname/voice-agent/internal/fileindex"
)

// errNoIndex is returned by the delegating helpers when no persistent index has
// been installed yet.
var errNoIndex = errors.New("file index not available")

// Resolve returns the single best path for query from the persistent index, or
// false if none is confident (or the index isn't installed yet).
func Resolve(query string) (string, bool) {
	mu.RLock()
	idx := ix
	mu.RUnlock()
	if idx == nil {
		return "", false
	}
	return idx.Resolve(query, fileindex.KindAny)
}

// RecordOpen tells the persistent index that path was opened (usage learning).
// A no-op when no index is installed.
func RecordOpen(path string) {
	mu.RLock()
	idx := ix
	mu.RUnlock()
	if idx != nil {
		idx.RecordOpen(path)
	}
}

// Remember pins an explicit alias key to a path in the persistent index.
func Remember(key, path string) error {
	mu.RLock()
	idx := ix
	mu.RUnlock()
	if idx == nil {
		return errNoIndex
	}
	return idx.Remember(key, path)
}

type FileRecord struct {
	Path string
	Name string
}

// index and ready back the legacy in-memory walk (InitIndexer/SearchFiles). They
// remain as a fallback for callers/tests that have not yet been migrated to the
// persistent fileindex. Once SetIndex has installed a *fileindex.Index,
// SearchFiles delegates there instead. Both are written by the background walk
// and read from arbitrary goroutines, so access is guarded.
var (
	mu    sync.RWMutex
	index map[string][]FileRecord
	ready bool

	ix *fileindex.Index // set via SetIndex; nil until main wires it
)

// SetIndex installs the persistent fileindex.Index that SearchFiles delegates to.
// Passing nil reverts to the legacy in-memory index.
func SetIndex(idx *fileindex.Index) {
	mu.Lock()
	ix = idx
	mu.Unlock()
}

// Ready reports whether a file index is available to serve queries — either the
// persistent fileindex (once installed) or the completed legacy walk.
func Ready() bool {
	mu.RLock()
	defer mu.RUnlock()
	return ix != nil || ready
}

// InitIndexer walks rootDir in the background and publishes the finished index
// atomically. Retained as a fallback for callers not yet migrated to the
// persistent fileindex (main wiring moves to fileindex in Task 8). When a
// persistent index has been installed via SetIndex, SearchFiles ignores this
// legacy index.
func InitIndexer(roots ...string) {
	mu.Lock()
	index = nil
	ready = false
	mu.Unlock()

	go func() {
		local := make(map[string][]FileRecord)
		seen := make(map[string]bool) // a root nested inside another must not be walked twice

		for _, root := range roots {
			if root == "" {
				continue
			}
			abs, err := filepath.Abs(root)
			if err != nil {
				abs = root
			}
			if seen[strings.ToLower(abs)] {
				continue
			}
			seen[strings.ToLower(abs)] = true

			log.Printf("Indexing %s...", abs)
			_ = filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				// Skip hidden directories and large system folders to speed up indexing.
				if info.IsDir() && (strings.HasPrefix(info.Name(), ".") || info.Name() == "AppData" || info.Name() == "Windows" ||
					info.Name() == "node_modules" || info.Name() == "$RECYCLE.BIN") {
					return filepath.SkipDir
				}
				name := strings.ToLower(info.Name())
				local[name] = append(local[name], FileRecord{Path: path, Name: info.Name()})
				return nil
			})
		}

		mu.Lock()
		index = local
		ready = true
		mu.Unlock()

		log.Printf("File indexer finished. Indexed %d distinct names across %d root(s).", len(local), len(seen))
	}()
}
