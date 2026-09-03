package fileindex

import ("os"; "path/filepath"; "strings"; "testing")

func TestExtractTextPlain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "note.md")
	os.WriteFile(p, []byte("# Startup idea\nA voice assistant business."), 0o644)
	txt, ok := extractText(p)
	if !ok || !strings.Contains(txt, "Startup idea") { t.Fatalf("extract md: %q ok=%v", txt, ok) }
}
func TestExtractRejectsBinary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.exe")
	os.WriteFile(p, []byte{0,1,2}, 0o644)
	if _, ok := extractText(p); ok { t.Fatal("exe should not be embeddable") }
	if isEmbeddable("dll") { t.Fatal("dll should not be embeddable") }
}
