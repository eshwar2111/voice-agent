package ambient

import "testing"

func TestClassifyDownload(t *testing.T) {
	cases := map[string]string{ // filename -> expected Kind ("" = no match)
		"report.zip":        "archive",
		"photo.PNG":         "image",
		"setup.exe":         "installer",
		"notes.txt":         "",
		"movie.part":        "", // partial download ignored
		"archive.zip.crdownload": "",
	}
	for name, want := range cases {
		m, ok := ClassifyDownload(name)
		if want == "" {
			if ok {
				t.Errorf("%q should not classify (got %q)", name, m.Kind)
			}
			continue
		}
		if !ok || m.Kind != want {
			t.Errorf("%q -> want %q, got ok=%v kind=%q", name, want, ok, m.Kind)
		}
	}
}

func TestClassifyClipboard(t *testing.T) {
	m, ok := ClassifyClipboard("https://github.com/x/y")
	if !ok || m.Kind != "url" || m.URL != "https://github.com/x/y" {
		t.Errorf("url classify: ok=%v %+v", ok, m)
	}
	m, ok = ClassifyClipboard("panic: runtime error: index out of range\n\tmain.go:12")
	if !ok || m.Kind != "error" {
		t.Errorf("error classify: ok=%v %+v", ok, m)
	}
	if _, ok := ClassifyClipboard("just some ordinary text"); ok {
		t.Error("ordinary text must not classify")
	}
}
