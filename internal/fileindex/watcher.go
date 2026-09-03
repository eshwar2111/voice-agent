package fileindex

import (
	"io/fs"
	"log"
	"path/filepath"
	"strings"
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
			if _, uerr := ix.store.Upsert(f); uerr != nil {
				log.Printf("fileindex: upsert %s: %v", path, uerr)
				return nil
			}
			seenPaths[path] = true
			return nil
		})
	}

	ix.reconcile(seenPaths)
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
