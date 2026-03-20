package search

import "strings"

func SearchFiles(query string) []FileRecord {
	if !IsReady {
		return nil
	}

	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}

	var results []FileRecord

	for name, files := range index {
		if strings.Contains(name, query) {
			results = append(results, files...)
		}
	}

	if len(results) > 20 {
		return results[:20] // limit for UI friendliness
	}

	return results
}
