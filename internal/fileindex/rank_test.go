package fileindex

import "testing"

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
