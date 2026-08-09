package search

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type FileRecord struct {
	Path string
	Name string
}

// index and ready are written by the background walk in InitIndexer and read
// by SearchFiles from arbitrary goroutines (the resolver's file matcher, the
// explorer tool). Both were previously unsynchronised: a search running while
// the walk was still populating the map is a concurrent map read/write, which
// Go treats as a FATAL runtime error and kills the process. It is reachable in
// normal use — indexing a user profile takes seconds, and the first thing
// someone does is speak a command.
var (
	mu    sync.RWMutex
	index map[string][]FileRecord
	ready bool
)

// Ready reports whether the initial index walk has completed.
func Ready() bool {
	mu.RLock()
	defer mu.RUnlock()
	return ready
}

// InitIndexer walks rootDir in the background and publishes the finished index
// atomically. The walk builds into a local map and swaps it in under the lock,
// so readers never observe a half-populated index and the lock is not held for
// the duration of a filesystem walk.
func InitIndexer(rootDir string) {
	mu.Lock()
	index = nil
	ready = false
	mu.Unlock()

	go func() {
		log.Printf("Starting file indexer on %s...", rootDir)
		local := make(map[string][]FileRecord)

		_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			// Skip hidden directories and large system folders to speed up indexing.
			if info.IsDir() && (strings.HasPrefix(info.Name(), ".") || info.Name() == "AppData" || info.Name() == "Windows") {
				return filepath.SkipDir
			}
			name := strings.ToLower(info.Name())
			local[name] = append(local[name], FileRecord{Path: path, Name: info.Name()})
			return nil
		})

		mu.Lock()
		index = local
		ready = true
		mu.Unlock()

		log.Printf("File indexer finished. Indexed %d distinct names in %s.", len(local), rootDir)
	}()
}
