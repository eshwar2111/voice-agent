package tools

import "testing"

// "open voice agent folder" names the directory "Voice Agent" — the trailing
// noun is how people speak, not part of the name. Leaving it in makes the
// index lookup miss every time.
func TestCleanFolderQuery(t *testing.T) {
	cases := map[string]string{
		"voice agent folder":    "voice agent",
		"Voice Agent Folder":    "voice agent",
		"downloads directory":   "downloads",
		"the downloads folder":  "downloads",
		"my documents":          "documents",
		"projects":              "projects",
		"":                      "",
		"folder":                "",
	}
	for in, want := range cases {
		if got := cleanFolderQuery(in); got != want {
			t.Errorf("cleanFolderQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

// A shorter path is a better match than a deeply nested one, and an exact
// name match beats a substring.
func TestPickBestDir(t *testing.T) {
	cands := []string{
		`E:\Projects\archive\old\Voice Agent Backup`,
		`E:\Voice Agent`,
		`E:\Projects\Voice Agent Notes`,
	}
	if got := pickBestDir(cands, "voice agent"); got != `E:\Voice Agent` {
		t.Errorf("pickBestDir = %q, want the exact-name, shortest-path candidate", got)
	}
	if got := pickBestDir(nil, "x"); got != "" {
		t.Errorf("pickBestDir(nil) = %q, want empty", got)
	}
}
