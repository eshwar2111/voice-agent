package fileindex

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// builtinSkipDirs are directory names always skipped during scans regardless of
// the user-configured exclude list: caches, VCS metadata, and heavy system
// trees that never hold user documents.
var builtinSkipDirs = map[string]bool{
	"node_modules":              true,
	".git":                      true,
	"$RECYCLE.BIN":              true,
	"AppData":                   true,
	"Windows":                   true,
	"Program Files":             true,
	"$WinREAgent":               true,
	"System Volume Information": true,
}

// scan walks the configured roots, upserting every file and directory it finds
// (honoring excludes + hidden/system skips), then reconciles by deleting rows
// whose path no longer exists on disk.
func (ix *Index) scan() {
	seenRoots := make(map[string]bool)
	seenPaths := make(map[string]bool)

	for _, root := range ix.roots {
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		key := strings.ToLower(abs)
		if seenRoots[key] {
			continue // a root nested inside another must not be walked twice
		}
		seenRoots[key] = true

		log.Printf("fileindex: scanning %s...", abs)
		_ = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := d.Name()

			if d.IsDir() {
				// Skip excluded, hidden, and system directories entirely.
				if path != abs && ix.skipDir(name) {
					return filepath.SkipDir
				}
			}

			f, ok := ix.fileFromEntry(path, d)
			if !ok {
				return nil
			}
			if _, uerr := ix.upsertFile(f); uerr != nil {
				log.Printf("fileindex: upsert %s: %v", path, uerr)
				return nil
			}
			seenPaths[path] = true
			return nil
		})
	}

	ix.reconcile(seenPaths)
}

// watch runs a live fsnotify watcher over the configured roots, translating
// filesystem events into incremental store upserts (create/write) and deletes
// (remove/rename-away). It recursively watches subdirectories, adding newly
// created ones on the fly, and debounces rapid write bursts per path. It runs
// until ctx is cancelled.
func (ix *Index) watch(ctx context.Context) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("fileindex: watcher: %v", err)
		return
	}
	defer w.Close()

	// Register each root and every existing subdirectory (honoring skips).
	for _, root := range ix.roots {
		if root == "" {
			continue
		}
		abs, aerr := filepath.Abs(root)
		if aerr != nil {
			abs = root
		}
		ix.addWatchTree(w, abs)
	}

	deb := newDebouncer(300*time.Millisecond, func(path string) {
		ix.handleUpsert(w, path)
	})
	defer deb.stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			ix.handleEvent(w, event, deb)
		case werr, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("fileindex: watch error: %v", werr)
		}
	}
}

// handleEvent routes a single fsnotify event.
func (ix *Index) handleEvent(w *fsnotify.Watcher, event fsnotify.Event, deb *debouncer) {
	path := event.Name

	// Removals/renames-away: drop the row (and cancel any pending upsert).
	if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		deb.cancel(path)
		if err := ix.store.DeleteByPath(path); err != nil {
			log.Printf("fileindex: watch delete %s: %v", path, err)
		}
		return
	}

	if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
		// A newly created directory must be watched (and its contents scanned).
		if event.Op&fsnotify.Create != 0 {
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				if ix.pathExcluded(path) {
					return
				}
				ix.addWatchTree(w, path)
				return
			}
		}
		deb.trigger(path)
	}
}

// handleUpsert stats a path and upserts it, unless excluded or gone.
func (ix *Index) handleUpsert(w *fsnotify.Watcher, path string) {
	if ix.pathExcluded(path) {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		// Vanished between event and handling: ensure it's not stale.
		_ = ix.store.DeleteByPath(path)
		return
	}
	if info.IsDir() {
		ix.addWatchTree(w, path)
		return
	}
	f := ix.fileFromInfo(path, info)
	if _, uerr := ix.upsertFile(f); uerr != nil {
		log.Printf("fileindex: watch upsert %s: %v", path, uerr)
	}
}

// addWatchTree adds path and every non-skipped subdirectory to the watcher,
// upserting files it encounters (so a freshly created directory tree is indexed).
func (ix *Index) addWatchTree(w *fsnotify.Watcher, path string) {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != path && ix.skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if aerr := w.Add(p); aerr != nil {
				log.Printf("fileindex: watch add %s: %v", p, aerr)
			}
			return nil
		}
		if f, ok := ix.fileFromEntry(p, d); ok {
			if _, uerr := ix.upsertFile(f); uerr != nil {
				log.Printf("fileindex: watch scan upsert %s: %v", p, uerr)
			}
		}
		return nil
	})
}

// pathExcluded reports whether any directory component of path *below its
// containing root* is excluded, hidden, or a built-in system dir. Components at
// or above the root itself are never checked (the root may legitimately live
// under a normally-skipped dir, e.g. a temp dir under AppData).
func (ix *Index) pathExcluded(path string) bool {
	root, ok := ix.containingRoot(path)
	if !ok {
		return true // outside every watched root
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	// Check every segment except the last (the file/dir's own base name).
	segs := strings.Split(rel, string(filepath.Separator))
	for _, seg := range segs[:len(segs)-1] {
		if seg == "" || seg == "." {
			continue
		}
		if ix.skipDir(seg) {
			return true
		}
	}
	return false
}

// containingRoot returns the watched root that contains path (case-insensitive
// prefix match on Windows), if any.
func (ix *Index) containingRoot(path string) (string, bool) {
	lp := strings.ToLower(path)
	for _, root := range ix.roots {
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		la := strings.ToLower(abs)
		if lp == la || strings.HasPrefix(lp, la+string(filepath.Separator)) {
			return abs, true
		}
	}
	return "", false
}

// fileFromInfo builds a File from a stat result (watcher path, no DirEntry).
func (ix *Index) fileFromInfo(path string, info os.FileInfo) File {
	f := File{
		Path:   path,
		Name:   info.Name(),
		Parent: filepath.Base(filepath.Dir(path)),
		IsDir:  info.IsDir(),
	}
	if !info.IsDir() {
		f.Ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		f.Size = info.Size()
	}
	mod := info.ModTime().Unix()
	f.ModifiedAt = mod
	f.CreatedAt = mod
	return f
}

// debouncer coalesces rapid triggers for the same key, firing fn once per key
// after the quiet period elapses.
type debouncer struct {
	mu    sync.Mutex
	delay time.Duration
	fn    func(string)
	timers map[string]*time.Timer
	closed bool
}

func newDebouncer(delay time.Duration, fn func(string)) *debouncer {
	return &debouncer{delay: delay, fn: fn, timers: make(map[string]*time.Timer)}
}

func (d *debouncer) trigger(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	if t, ok := d.timers[key]; ok {
		t.Stop()
	}
	d.timers[key] = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		delete(d.timers, key)
		closed := d.closed
		d.mu.Unlock()
		if !closed {
			d.fn(key)
		}
	})
}

func (d *debouncer) cancel(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.timers[key]; ok {
		t.Stop()
		delete(d.timers, key)
	}
}

func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	for k, t := range d.timers {
		t.Stop()
		delete(d.timers, k)
	}
}

// maxFallbackWalk bounds the live safety-net walk so an index miss can never
// become a multi-second whole-disk scan.
const maxFallbackWalk = 60000

// filesystemFallback is the last-resort safety net: when the index returns
// nothing confident, walk the configured roots LIVE and return files whose name
// contains any significant query token (separators normalized). Guarantees an
// existing file under a root is found even if the index missed it (stale, a
// dropped watcher event, a just-created file). Bounded in files walked and
// results returned so it stays fast; anything it finds is opportunistically
// indexed so the next lookup is a fast hit.
func (ix *Index) filesystemFallback(query string, kind Kind) []File {
	toks := significantTokens(query)
	if len(toks) == 0 {
		return nil
	}
	repl := strings.NewReplacer("_", " ", "-", " ", ".", " ")
	out := make([]File, 0, 8)
	walked := 0
	for _, root := range ix.roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if walked >= maxFallbackWalk || len(out) >= 25 {
				return filepath.SkipAll
			}
			walked++
			if d.IsDir() {
				if ix.skipDir(d.Name()) {
					return filepath.SkipDir
				}
				if kind == KindFile {
					return nil
				}
			} else if kind == KindFolder {
				return nil
			}
			norm := repl.Replace(strings.ToLower(d.Name()))
			for _, t := range toks {
				if strings.Contains(norm, t) {
					if f, ok := ix.fileFromEntry(p, d); ok {
						out = append(out, f)
						_, _ = ix.upsertFile(f) // index for next time
					}
					break
				}
			}
			return nil
		})
	}
	return out
}

// skipDir reports whether a directory with the given base name should be skipped.
func (ix *Index) skipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	if builtinSkipDirs[name] {
		return true
	}
	for _, ex := range ix.exclude {
		if strings.EqualFold(ex, name) {
			return true
		}
	}
	return false
}

// fileFromEntry builds a File from a walk entry, populating stat-derived fields.
func (ix *Index) fileFromEntry(path string, d fs.DirEntry) (File, bool) {
	info, err := d.Info()
	if err != nil {
		return File{}, false
	}

	f := File{
		Path:   path,
		Name:   d.Name(),
		Parent: filepath.Base(filepath.Dir(path)),
		IsDir:  d.IsDir(),
	}
	if !d.IsDir() {
		f.Ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		f.Size = info.Size()
	}
	mod := info.ModTime().Unix()
	f.ModifiedAt = mod
	f.CreatedAt = mod
	return f, true
}

// reconcile deletes rows for paths that were not seen during the scan.
func (ix *Index) reconcile(seen map[string]bool) {
	all, err := ix.store.AllPaths()
	if err != nil {
		log.Printf("fileindex: reconcile: %v", err)
		return
	}
	for path := range all {
		if !seen[path] {
			if derr := ix.store.DeleteByPath(path); derr != nil {
				log.Printf("fileindex: reconcile delete %s: %v", path, derr)
			}
		}
	}
}
