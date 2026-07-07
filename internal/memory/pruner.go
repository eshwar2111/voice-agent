package memory

import (
	"database/sql"
	"log"
	"time"
)

// PruneMemories removes low-importance memories that are older than the specified duration.
// High-importance memories (importance >= 4) are preserved indefinitely.
func PruneMemories(db *sql.DB, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).Format(time.RFC3339)
	
	// Delete low-importance memories (0-2) older than cutoff
	res, err := db.Exec(`DELETE FROM memories WHERE importance <= 2 AND timestamp < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	
	rows, _ := res.RowsAffected()
	if rows > 0 {
		log.Printf("Memory Pruner: Removed %d old low-importance memories.", rows)
	}
	
	return rows, nil
}

// StartPruningScheduler runs the memory pruner daily.
func StartPruningScheduler(db *sql.DB) {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		for range ticker.C {
			_, err := PruneMemories(db, 30*24*time.Hour) // 30 days
			if err != nil {
				log.Printf("Memory Pruner Error: %v", err)
			}
		}
	}()
}
