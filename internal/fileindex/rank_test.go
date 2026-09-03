package fileindex

import "testing"

func TestTextMatchTokenOverlap(t *testing.T) {
	// The reported miss: multi-word query vs underscored filename.
	if got := textMatch("startup brainstorm", "startup_brainstorm.txt"); got < 0.9 {
		t.Errorf(`"startup brainstorm" vs startup_brainstorm.txt = %f, want ~1.0`, got)
	}
	// Extra filler words must NOT drop the match (partial overlap still scores).
	if got := textMatch("next startup brainstorm idea", "startup_brainstorm.txt"); got < 0.55 {
		t.Errorf("extra words should still match: %f", got)
	}
	// Stopwords ignored: "open my resume file" keys on "resume".
	if got := textMatch("open my resume file", "Resume_2026.pdf"); got < 0.55 {
		t.Errorf("stopword-heavy query should match on resume: %f", got)
	}
	// Word order independent.
	if got := textMatch("brainstorm startup", "startup_brainstorm.txt"); got < 0.9 {
		t.Errorf("word order should not matter: %f", got)
	}
	// A totally unrelated name scores 0.
	if got := textMatch("startup brainstorm", "budget_q3.xlsx"); got != 0 {
		t.Errorf("unrelated name should be 0, got %f", got)
	}
}

func TestLatestResumeWins(t *testing.T) {
	now := int64(1_700_000_000)
	day := int64(86400)
	newer := Candidate{File: File{Name: "Resume_2026.pdf", ModifiedAt: now - day}, TextMatch: 0.9}
	older := Candidate{File: File{Name: "Resume_old.pdf", ModifiedAt: now - 800*day}, TextMatch: 0.9}
	if rankScore(newer, now) <= rankScore(older, now) {
		t.Fatalf("recent resume should outrank old: %f vs %f", rankScore(newer, now), rankScore(older, now))
	}
}

func TestUsageAndAliasBoost(t *testing.T) {
	now := int64(1_700_000_000)
	plain := Candidate{File: File{Name: "cv.pdf", ModifiedAt: now}, TextMatch: 0.8}
	used := Candidate{File: File{Name: "cv.pdf", ModifiedAt: now, UsageScore: 10}, TextMatch: 0.8}
	if used2 := rankScore(used, now); used2 <= rankScore(plain, now) {
		t.Fatalf("usage should boost: %f", used2)
	}
	aliased := Candidate{File: File{Name: "cv.pdf", ModifiedAt: now}, TextMatch: 0.8, AliasMatch: 1}
	if rankScore(aliased, now) <= rankScore(plain, now) {
		t.Fatal("alias should boost")
	}
}
