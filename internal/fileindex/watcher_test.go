package fileindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchPicksUpNewFile(t *testing.T) {
	root := t.TempDir()
	ix, _ := New(filepath.Join(t.TempDir(), "idx.db"), []string{root}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	// Stop the watcher and close the store before t.TempDir cleanup runs, so the
	// open SQLite handle does not block RemoveAll on Windows.
	defer func() { cancel(); ix.Close() }()
	ix.scan()
	go ix.watch(ctx)
	time.Sleep(150 * time.Millisecond) // let the watcher register

	os.WriteFile(filepath.Join(root, "notes.md"), []byte("x"), 0o644)
	// poll up to 2s for the event to land
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(ix.Search("notes", KindAny)) > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("new file never indexed by watcher")
}
