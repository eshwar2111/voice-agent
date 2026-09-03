package fileindex

import (
	"os"
	"path/filepath"
	"testing"
)

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
