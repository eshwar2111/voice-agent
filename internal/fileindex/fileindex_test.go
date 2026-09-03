package fileindex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMultiWordAndExtraWords(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "startup_brainstorm.txt"), []byte("x"), 0o644)

	ix, err := New(filepath.Join(t.TempDir(), "idx.db"), []string{root}, nil, nil)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { ix.Close() })
	ix.scan()

	for _, q := range []string{
		"startup brainstorm",            // the reported miss (underscore vs space)
		"next startup brainstorm idea",  // extra filler words
		"open my startup brainstorm",    // stopwords
		"brainstorm startup",            // word order
	} {
		got, ok := ix.Resolve(q, KindFile)
		if !ok || filepath.Base(got) != "startup_brainstorm.txt" {
			t.Errorf("Resolve(%q) = %q ok=%v; want startup_brainstorm.txt", q, got, ok)
		}
	}
}

func TestResolveFilesystemFallbackWhenNotIndexed(t *testing.T) {
	root := t.TempDir()
	ix, err := New(filepath.Join(t.TempDir(), "idx.db"), []string{root}, nil, nil)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { ix.Close() })
	ix.scan() // empty at scan time
	// Create the file AFTER the scan and DON'T notify the watcher — the live
	// filesystem safety net must still find it.
	os.WriteFile(filepath.Join(root, "quarterly_report.pdf"), []byte("x"), 0o644)
	got, ok := ix.Resolve("quarterly report", KindFile)
	if !ok || filepath.Base(got) != "quarterly_report.pdf" {
		t.Errorf("filesystem fallback failed: got %q ok=%v", got, ok)
	}
}

func TestScanIndexesFiles(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Resume_2026.pdf"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(root, "node_modules"), 0o755)
	os.WriteFile(filepath.Join(root, "node_modules", "junk.js"), []byte("x"), 0o644)

	ix, err := New(filepath.Join(t.TempDir(), "idx.db"), []string{root}, []string{"node_modules"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() }) // release the DB handle so TempDir cleanup can unlink it on Windows
	ix.scan()

	res := ix.Search("resume", KindAny)
	if len(res) == 0 || filepath.Base(res[0].Path) != "Resume_2026.pdf" {
		t.Fatalf("scan/search: %+v", res)
	}
	if len(ix.Search("junk", KindAny)) != 0 {
		t.Fatal("excluded dir was indexed")
	}
}
