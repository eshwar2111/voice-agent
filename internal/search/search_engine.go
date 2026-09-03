package search

import (
	"strings"

	"github.com/yourname/voice-agent/internal/fileindex"
)

// SearchFiles returns indexed entries whose name matches query.
//
// When a persistent fileindex.Index has been installed via SetIndex, the query
// delegates there (mapping fileindex results to search.FileRecord). Otherwise it
// falls back to the legacy in-memory scan populated by InitIndexer, which holds
// the read lock for the whole scan so a search concurrent with re-indexing is
// safe rather than a fatal concurrent map read/write. Returns nil when no index
// is available yet.
func SearchFiles(query string) []FileRecord {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}

	mu.RLock()
	idx := ix
	mu.RUnlock()

	if idx != nil {
		recs := idx.Search(query, fileindex.KindAny)
		out := make([]FileRecord, 0, len(recs))
		for _, r := range recs {
			out = append(out, FileRecord{Path: r.Path, Name: r.Name})
		}
		return out
	}

	mu.RLock()
	defer mu.RUnlock()

	if !ready {
		return nil
	}

	var results []FileRecord
	for name, files := range index {
		if strings.Contains(name, query) {
			results = append(results, files...)
			if len(results) >= 20 {
				return results[:20] // limit for UI friendliness
			}
		}
	}
	return results
}
