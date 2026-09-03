// This file implements the ranking pipeline (Task 4 of
// docs/superpowers/plans/2026-09-03-file-intelligence-index.md): a pure
// rankScore combining text match, recency, usage, and alias signals, wired
// into Index.Search/Resolve.
package fileindex

import (
	"math"
	"os"
	"path/filepath"
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

// fileStopwords are filler words that must not drive or dilute file matching —
// "open my latest resume file" should match on "resume", not on "my"/"file".
var fileStopwords = map[string]bool{
	"my": true, "the": true, "a": true, "an": true, "of": true, "to": true,
	"for": true, "about": true, "that": true, "this": true, "please": true,
	"open": true, "find": true, "show": true, "get": true, "give": true,
	"file": true, "files": true, "document": true, "documents": true,
	"doc": true, "docs": true, "folder": true, "me": true, "up": true,
}

// splitTokens lowercases and splits on spaces and every filename separator.
func splitTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == '_' || r == '-' || r == '.' || r == '/' || r == '\\'
	})
}

// significantTokens drops stopwords and 1-char noise; falls back to all tokens
// if everything was a stopword (e.g. a query that is only filler).
func significantTokens(s string) []string {
	all := splitTokens(s)
	sig := make([]string, 0, len(all))
	for _, t := range all {
		if len(t) > 1 && !fileStopwords[t] {
			sig = append(sig, t)
		}
	}
	if len(sig) == 0 {
		return all
	}
	return sig
}

// textMatch scores name against query by TOKEN OVERLAP — robust to word order,
// to filename separators (space/_/-/.), and to extra filler words in the query.
// Exact/prefix on the joined significant query still score highest.
func textMatch(query, name string) float64 {
	lower := strings.ToLower(name)
	base := strings.TrimSuffix(lower, extOf(lower))
	nameToks := splitTokens(base)
	if len(nameToks) == 0 {
		return 0
	}
	nameSet := make(map[string]bool, len(nameToks))
	for _, t := range nameToks {
		nameSet[t] = true
	}
	qToks := significantTokens(query)
	if len(qToks) == 0 {
		return 0
	}
	joinedName, joinedQ := strings.Join(nameToks, " "), strings.Join(qToks, " ")
	switch {
	case joinedName == joinedQ:
		return 1.0
	case strings.HasPrefix(joinedName, joinedQ):
		return 0.9
	}
	matched := 0
	for _, t := range qToks {
		if nameSet[t] {
			matched++
		}
	}
	if matched == 0 {
		if strings.Contains(joinedName, joinedQ) {
			return 0.5
		}
		return 0
	}
	// 0.55 (one of several query tokens present) → 0.95 (all present).
	return 0.55 + 0.4*(float64(matched)/float64(len(qToks)))
}

func extOf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// hotLookup checks the Tier-1 cache for an exact normalized alias/memory hit,
// returning its path only if it still exists on disk and matches kind.
func (ix *Index) hotLookup(query string, kind Kind) (string, bool) {
	if ix.hot == nil {
		return "", false
	}
	path, ok := ix.hot.lookup(normalizeKey(query))
	if !ok {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	switch kind {
	case KindFile:
		if info.IsDir() {
			return "", false
		}
	case KindFolder:
		if !info.IsDir() {
			return "", false
		}
	}
	return path, true
}

// Search returns indexed entries matching query, narrowed by kind, ranked by
// rankScore (text + recency + usage + alias) and sorted best-first. A Tier-1 hot
// cache hit (explicit memory / alias) short-circuits to that single path.
// searchCandidates is the shared ranked engine used by BOTH Search and Resolve:
// FTS/fuzzy metadata candidates, re-ranked, plus the live filesystem safety net
// merged in when nothing indexed matched confidently. (Hot-cache and semantic
// are handled by the callers so each can short-circuit / apply its own rule.)
func (ix *Index) searchCandidates(query string, kind Kind) []Candidate {
	files, err := ix.store.SearchFTS(query, 50)
	if err != nil {
		files = nil
	}
	aliasSet := ix.aliasMatches(query)
	now := nowUnix()

	score := func(fs []File) []Candidate {
		cs := make([]Candidate, 0, len(fs))
		for _, f := range fs {
			if kind == KindFile && f.IsDir || kind == KindFolder && !f.IsDir {
				continue
			}
			cs = append(cs, Candidate{
				File:       f,
				TextMatch:  textMatch(query, f.Name),
				AliasMatch: aliasMatchScore(aliasSet, f.ID),
			})
		}
		return cs
	}
	byScore := func(cs []Candidate) {
		sort.SliceStable(cs, func(i, j int) bool { return rankScore(cs[i], now) > rankScore(cs[j], now) })
	}

	cands := score(files)
	byScore(cands)

	// Safety net: a live filesystem walk of the roots when nothing indexed
	// matched confidently — an existing file is still found even if the index
	// missed it (stale, a dropped watcher event, a just-created file).
	if len(cands) == 0 || rankScore(cands[0], now) < resolveThreshold {
		cands = append(cands, score(ix.filesystemFallback(query, kind))...)
		byScore(cands)
	}
	return cands
}

func (ix *Index) Search(query string, kind Kind) []FileRecord {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	if path, ok := ix.hotLookup(query, kind); ok {
		return []FileRecord{{Path: path, Name: filepath.Base(path)}}
	}

	cands := ix.searchCandidates(query, kind)
	now := nowUnix()

	// Tier 3 — semantic fallback: only when metadata+filesystem found nothing
	// confident AND an embedder is configured.
	if ix.embedder != nil && (len(cands) == 0 || rankScore(cands[0], now) < resolveThreshold) {
		if sem := ix.semanticSearch(query, kind); len(sem) > 0 {
			return sem
		}
	}

	out := make([]FileRecord, 0, len(cands))
	for _, c := range cands {
		out = append(out, FileRecord{Path: c.File.Path, Name: c.File.Name})
	}
	return out
}

// aliasMatchScore is 1.0 when the file id is in the alias-match set, else 0.
func aliasMatchScore(set map[int64]bool, id int64) float64 {
	if set[id] {
		return 1.0
	}
	return 0
}

// Resolve returns the single best path for query narrowed by kind, or false
// if no candidate clears resolveThreshold.
func (ix *Index) Resolve(query string, kind Kind) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", false
	}

	if path, ok := ix.hotLookup(query, kind); ok {
		return path, true
	}

	cands := ix.searchCandidates(query, kind)
	now := nowUnix()
	if len(cands) > 0 && rankScore(cands[0], now) >= resolveThreshold {
		return cands[0].File.Path, true
	}

	// Tier 3 — semantic fallback, mirroring Search: only when metadata +
	// filesystem produced nothing confident and an embedder is configured.
	if ix.embedder != nil {
		if sem := ix.semanticSearch(query, kind); len(sem) > 0 {
			return sem[0].Path, true
		}
	}
	return "", false
}
