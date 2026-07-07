package memory

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Retriever performs text searches over the memory store using SQLite LIKE queries.
// This is model-agnostic and works with the default go-sqlite3 build (no FTS5 required).
type Retriever struct {
	db *sql.DB
}

// NewRetriever creates a new Retriever using the provided database connection.
func NewRetriever(db *sql.DB) *Retriever {
	return &Retriever{db: db}
}

// Search finds memories relevant to the given query using token-based LIKE matching.
// Each word in the query is matched independently; results are ranked by importance then recency.
// Returns up to `limit` results.
func (r *Retriever) Search(query string, limit int) ([]Memory, error) {
	if query == "" {
		return nil, nil
	}

	tokens := extractTokens(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	// Prefer exact phrase matches first, then token matches ranked by importance/recency.
	clauses := make([]string, 0, len(tokens)+1)
	args := make([]interface{}, 0, len(tokens)+2)
	normalizedQuery := strings.TrimSpace(query)
	if normalizedQuery != "" {
		clauses = append(clauses, "content LIKE ?")
		args = append(args, "%"+normalizedQuery+"%")
	}
	for i, tok := range tokens {
		_ = i
		clauses = append(clauses, "content LIKE ?")
		args = append(args, "%"+tok+"%")
	}
	args = append(args, limit)

	query_sql := fmt.Sprintf(`
		SELECT id, type, content, importance, timestamp
		FROM memories
		WHERE %s
		ORDER BY importance DESC, timestamp DESC
		LIMIT ?`,
		strings.Join(clauses, " OR "),
	)

	rows, err := r.db.Query(query_sql, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: search failed: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

// SearchByType returns recent memories filtered by type.
func (r *Retriever) SearchByType(memType MemoryType, limit int) ([]Memory, error) {
	rows, err := r.db.Query(
		`SELECT id, type, content, importance, timestamp FROM memories WHERE type = ? ORDER BY timestamp DESC LIMIT ?`,
		string(memType), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// FormatForPrompt formats a list of memories as a concise markdown block
// suitable for injecting into an LLM prompt context.
func FormatForPrompt(memories []Memory) string {
	if len(memories) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("### Relevant Memories\n")
	for _, m := range memories {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", m.Type, m.Content))
	}
	return sb.String()
}

// extractTokens splits a query into meaningful search tokens.
// Filters out short/common words to improve match relevance.
func extractTokens(query string) []string {
	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "is": true,
		"in": true, "on": true, "at": true, "to": true,
		"of": true, "my": true, "me": true, "i": true,
		"what": true, "where": true, "how": true, "was": true,
	}

	words := strings.Fields(strings.ToLower(query))
	tokenSet := make(map[string]struct{}, len(words))
	for _, w := range words {
		// Strip punctuation
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, w)
		if len(clean) >= 3 && !stopWords[clean] {
			tokenSet[clean] = struct{}{}
		}
	}
	tokens := make([]string, 0, len(tokenSet))
	for token := range tokenSet {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}
