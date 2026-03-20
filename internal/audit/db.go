package audit

import (
	"database/sql"
	"encoding/json"
	"log"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	db   *sql.DB
	dbMu sync.Mutex
)

// InitDB initializes the SQLite database and creates the logs table if it doesn't exist.
func InitDB(filepath string) error {
	dbMu.Lock()
	defer dbMu.Unlock()

	var err error
	db, err = sql.Open("sqlite3", filepath)
	if err != nil {
		return err
	}

	createTableQuery := `
	CREATE TABLE IF NOT EXISTS audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		transcript TEXT,
		intent_name TEXT,
		parameters_json TEXT,
		status TEXT
	);
	`

	_, err = db.Exec(createTableQuery)
	if err != nil {
		log.Printf("Failed to create audit table: %v", err)
	}

	// Add transcript column if it doesn't exist (for existing databases)
	_, _ = db.Exec("ALTER TABLE audit_logs ADD COLUMN transcript TEXT")

	return err
}

// PruneOld removes logs older than maxAge.
func PruneOld(maxAge time.Duration) (int64, error) {
	dbMu.Lock()
	currentDB := db
	dbMu.Unlock()

	if currentDB == nil {
		return 0, nil
	}

	cutoff := time.Now().UTC().Add(-maxAge)
	res, err := currentDB.Exec(
		`DELETE FROM audit_logs WHERE timestamp < ?`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CloseDB closes the audit database connection.
func CloseDB() {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db != nil {
		db.Close()
		db = nil
	}
}

// LogAction inserts a record of the executed intent into the database.
func LogAction(transcript string, intentName string, params json.RawMessage, status string) error {
	dbMu.Lock()
	currentDB := db
	dbMu.Unlock()

	if currentDB == nil {
		log.Println("Audit DB not initialized, skipping log.")
		return nil
	}

	paramsStr := string(params)
	if len(params) == 0 {
		paramsStr = "{}"
	}

	insertQuery := `INSERT INTO audit_logs (transcript, intent_name, parameters_json, status) VALUES (?, ?, ?, ?)`
	_, err := currentDB.Exec(insertQuery, transcript, intentName, paramsStr, status)

	if err != nil {
		log.Printf("Failed to insert audit log: %v", err)
	}

	return err
}
