package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	dbOnce sync.Once
	dbConn *sql.DB
	dbErr  error
)

// DBPath returns the path to the Endless SQLite database.
func DBPath() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "endless", "endless.db")
}

// DB returns a connection to the Endless SQLite database.
func DB() (*sql.DB, error) {
	dbOnce.Do(func() {
		path := DBPath()
		dbConn, dbErr = sql.Open("sqlite", path)
		if dbErr != nil {
			return
		}
		dbConn.Exec("PRAGMA journal_mode=WAL")
		dbConn.Exec("PRAGMA foreign_keys=ON")
		migrate(dbConn)
	})
	return dbConn, dbErr
}

// migrate runs schema migrations for existing databases.
func migrate(db *sql.DB) {
	// Add title column to plan_items if missing
	rows, err := db.Query("PRAGMA table_info(plan_items)")
	if err == nil {
		hasTitle := false
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt *string
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == "title" {
				hasTitle = true
			}
		}
		rows.Close()
		if !hasTitle {
			db.Exec("ALTER TABLE plan_items ADD COLUMN title TEXT")
			db.Exec("UPDATE plan_items SET title = substr(task_text, 1, 80) WHERE title IS NULL")
		}
	}

	// Create task_dependencies table if missing
	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='task_dependencies'").Scan(&count)
	if err == nil && count == 0 {
		db.Exec(`CREATE TABLE IF NOT EXISTS task_dependencies (
			id INTEGER PRIMARY KEY,
			source_type TEXT NOT NULL CHECK (source_type IN ('task', 'plan', 'project')),
			source_id INTEGER NOT NULL,
			target_type TEXT NOT NULL CHECK (target_type IN ('task', 'plan', 'project')),
			target_id INTEGER NOT NULL,
			dep_type TEXT NOT NULL DEFAULT 'blocks' CHECK (dep_type IN ('blocks', 'needs')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
			UNIQUE(source_type, source_id, target_type, target_id)
		)`)
	}
}

// ProjectIDForPath looks up a registered project by working directory.
// Checks the exact path first, then walks up parent directories.
// Returns (id, true) if found, or creates/finds an anonymous project
// and returns (id, false) if the directory is not registered.
func ProjectIDForPath(dir string) (int64, bool, error) {
	db, err := DB()
	if err != nil {
		return 0, false, err
	}

	dir, err = filepath.Abs(dir)
	if err != nil {
		return 0, false, err
	}

	// Walk up looking for a registered project
	check := dir
	for {
		var id int64
		err := db.QueryRow(
			"SELECT id FROM projects WHERE path = ?", check,
		).Scan(&id)
		if err == nil {
			return id, true, nil
		}

		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}

	// No registered project found — create or find anonymous entry
	id, err := ensureAnonymousProject(db, dir)
	if err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// ensureAnonymousProject creates or retrieves an anonymous project
// for an unregistered directory.
func ensureAnonymousProject(db *sql.DB, dir string) (int64, error) {
	// Check if already exists
	var id int64
	err := db.QueryRow(
		"SELECT id FROM projects WHERE path = ? AND status = 'anonymous'",
		dir,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	// Create it
	name := fmt.Sprintf("_anon_%s", filepath.Base(dir))
	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	// Ensure unique name
	base := name
	for i := 2; ; i++ {
		var exists int
		db.QueryRow(
			"SELECT count(*) FROM projects WHERE name = ?", name,
		).Scan(&exists)
		if exists == 0 {
			break
		}
		name = fmt.Sprintf("%s_%d", base, i)
	}

	result, err := db.Exec(
		"INSERT INTO projects (name, path, status, created_at, updated_at) "+
			"VALUES (?, ?, 'anonymous', ?, ?)",
		name, dir, now, now,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
