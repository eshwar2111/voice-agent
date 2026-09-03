// This file implements the ranking pipeline (Task 4 of
// docs/superpowers/plans/2026-09-03-file-intelligence-index.md): a pure
// rankScore combining text match, recency, usage, and alias signals, wired
// into Index.Search/Resolve.
package fileindex

import (
	"math"
	"sort"
	"strings"
)

// Candidate is a scored search hit: a File plus the per-query signals that
// feed rankScore.
type Candidate struct {
	File       File
	TextMatch  float64 // 0..1: exact name=1.0, prefix=0.8, token=0.6, fuzzy substring=0.4
	AliasMatch float64 // 0 or 1 (Task 5 populates this from stored aliases)
}

// resolveThreshold is the minimum rankScore for Resolve to consider a
// candidate a confident match.
const resolveThreshold = 0.35

// recencyHalfLifeSeconds tunes the exponential decay so recency is ~1.0 for
// files modified today and decays toward 0 by ~2 years old.
const recencyHalfLifeSeconds = float64(180 * 24 * 3600) // ~180 days

// rankScore combines text match, recency, usage, and alias signals per the
// spec weights: 0.40*text + 0.25*recency + 0.20*usage + 0.15*alias.
func rankScore(c Candidate, now int64) float64 {
	return 0.40*c.TextMatch +
		0.25*recency(c.File.ModifiedAt, now) +
		0.20*usageNorm(c.File.UsageScore) +
		0.15*c.AliasMatch
}

// recency returns 1.0 for a file modified "now", decaying exponentially
// toward 0 as modified gets older (half-life recencyHalfLifeSeconds).
func recency(modifiedAt, now int64) float64 {
	if modifiedAt <= 0 {
		return 0
	}
	age := float64(now - modifiedAt)
	if age <= 0 {
		return 1
	}
	return math.Exp(-age / recencyHalfLifeSeconds)
}

// usageNorm squashes an unbounded usage_score into 0..1.
func usageNorm(score float64) float64 {
	if score <= 0 {
		return 0
	}
	v := score / 10
	if v > 1 {
		return 1
	}
	return v
}

// textMatch scores how well name matches the (already lowercased) query:
// exact match=1.0, prefix=0.8, whole-token=0.6, substring=0.4, else 0.
func textMatch(query, name string) float64 {
	q := strings.ToLower(strings.TrimSpace(query))
	n := strings.ToLower(name)
	if q == "" {
		return 0
	}
	base := strings.TrimSuffix(n, extOf(n))
	switch {
	case n == q || base == q:
		return 1.0
	case strings.HasPrefix(n, q) || strings.HasPrefix(base, q):
		return 0.8
	case hasToken(base, q):
		return 0.6
	case strings.Contains(n, q):
		return 0.4
	default:
		return 0
	}
}

func extOf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// hasToken reports whether q appears as a whole token in name once split on
// common filename separators (space, _, -, .).
func hasToken(name, q string) bool {
	tokens := strings.FieldsFunc(name, func(r rune) bool {
		return r == ' ' || r == '_' || r == '-' || r == '.'
	})
	for _, t := range tokens {
		if t == q {
			return true
		}
	}
	return false
}

// Search returns indexed entries matching query, narrowed by kind, ranked by
// rankScore (text + recency + usage + alias) and sorted best-first.
func (ix *Index) Search(query string, kind Kind) []FileRecord {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}

	files, err := ix.store.SearchFTS(query, 50)
	if err != nil {
		return nil
	}

	now := nowUnix()
	cands := make([]Candidate, 0, len(files))
	for _, f := range files {
		switch kind {
		case KindFile:
			if f.IsDir {
				continue
			}
		case KindFolder:
			if !f.IsDir {
				continue
			}
		}
		cands = append(cands, Candidate{
			File:      f,
			TextMatch: textMatch(query, f.Name),
		})
	}

	sort.SliceStable(cands, func(i, j int) bool {
		return rankScore(cands[i], now) > rankScore(cands[j], now)
	})

	out := make([]FileRecord, 0, len(cands))
	for _, c := range cands {
		out = append(out, FileRecord{Path: c.File.Path, Name: c.File.Name})
	}
	return out
}

// Resolve returns the single best path for query narrowed by kind, or false
// if no candidate clears resolveThreshold.
func (ix *Index) Resolve(query string, kind Kind) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", false
	}

	files, err := ix.store.SearchFTS(query, 50)
	if err != nil {
		return "", false
	}

	now := nowUnix()
	var best Candidate
	var bestScore float64
	found := false
	for _, f := range files {
		switch kind {
		case KindFile:
			if f.IsDir {
				continue
			}
		case KindFolder:
			if !f.IsDir {
				continue
			}
		}
		c := Candidate{File: f, TextMatch: textMatch(query, f.Name)}
		s := rankScore(c, now)
		if !found || s > bestScore {
			best, bestScore, found = c, s, true
		}
	}

	if !found || bestScore < resolveThreshold {
		return "", false
	}
	return best.File.Path, true
}
