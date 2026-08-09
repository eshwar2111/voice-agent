package search

import "strings"

// SearchFiles returns indexed entries whose name contains query.
//
// Holds the read lock for the whole scan: the index is swapped in wholesale by
// InitIndexer, so this can never observe a partially built map, and a search
// concurrent with re-indexing is safe rather than a fatal concurrent map
// read/write.
func SearchFiles(query string) []FileRecord {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
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
