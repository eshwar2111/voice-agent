package fileindex

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertGetSearch(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Upsert(File{Path: `C:\Users\E\Documents\Resume_2026.pdf`, Name: "Resume_2026.pdf", Ext: "pdf", Parent: "Documents"})
	if err != nil || id == 0 {
		t.Fatalf("Upsert: %v id=%d", err, id)
	}

	got, ok, err := s.GetByPath(`C:\Users\E\Documents\Resume_2026.pdf`)
	if err != nil || !ok || got.Name != "Resume_2026.pdf" {
		t.Fatalf("GetByPath: %+v ok=%v err=%v", got, ok, err)
	}

	res, err := s.SearchFTS("resume", 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(res) != 1 || res[0].Name != "Resume_2026.pdf" {
		t.Fatalf("SearchFTS got %+v", res)
	}
}

func TestUpsertIdempotentAndDelete(t *testing.T) {
	s := openTestStore(t)
	p := `C:\x\a.txt`
	id1, _ := s.Upsert(File{Path: p, Name: "a.txt", Ext: "txt"})
	id2, _ := s.Upsert(File{Path: p, Name: "a.txt", Ext: "txt", Size: 5})
	if id1 != id2 {
		t.Fatalf("upsert not idempotent: %d != %d", id1, id2)
	}
	if err := s.DeleteByPath(p); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := s.GetByPath(p); ok {
		t.Fatal("row still present after delete")
	}
}
