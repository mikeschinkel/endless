package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Add title column to plans if missing
	rows, err := db.Query("PRAGMA table_info(plans)")
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
			db.Exec("ALTER TABLE plans ADD COLUMN title TEXT")
			db.Exec("UPDATE plans SET title = substr(description, 1, 80) WHERE title IS NULL")
		}
	}

	// Add active_task_id column to ai_sessions if missing (legacy)
	rows, err = db.Query("PRAGMA table_info(ai_sessions)")
	if err == nil {
		hasActiveTaskID := false
		hasActiveGoalID := false
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt *string
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == "active_task_id" {
				hasActiveTaskID = true
			}
			if name == "active_goal_id" {
				hasActiveGoalID = true
			}
		}
		rows.Close()
		if !hasActiveTaskID && !hasActiveGoalID {
			db.Exec("ALTER TABLE ai_sessions ADD COLUMN active_goal_id INTEGER REFERENCES plans(id)")
		}
		if hasActiveTaskID && !hasActiveGoalID {
			db.Exec("ALTER TABLE ai_sessions ADD COLUMN active_goal_id INTEGER REFERENCES plans(id)")
			db.Exec("UPDATE ai_sessions SET active_goal_id = active_task_id WHERE active_task_id IS NOT NULL")
		}
	}

	// Add plan_file_path column to ai_sessions if missing
	rows, err = db.Query("PRAGMA table_info(ai_sessions)")
	if err == nil {
		hasPlanFilePath := false
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt *string
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == "plan_file_path" {
				hasPlanFilePath = true
			}
		}
		rows.Close()
		if !hasPlanFilePath {
			db.Exec("ALTER TABLE ai_sessions ADD COLUMN plan_file_path TEXT")
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

	// Fix broken FK references: active_task_id/active_goal_id reference plan_items instead of plans
	fixFK := false
	var createSQL string
	err = db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='ai_sessions'",
	).Scan(&createSQL)
	if err == nil && strings.Contains(createSQL, "plan_items") {
		fixFK = true
	}
	if fixFK {
		db.Exec("PRAGMA foreign_keys=OFF")
		db.Exec(`CREATE TABLE ai_sessions_new (
			id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL,
			project_id INTEGER,
			platform TEXT NOT NULL DEFAULT 'claude' CHECK (platform IN ('claude', 'codex')),
			state TEXT NOT NULL DEFAULT 'working' CHECK (state IN ('working', 'idle', 'needs_input', 'ended')),
			active_goal_id INTEGER,
			working_dir TEXT,
			transcript_path TEXT,
			plan_file_path TEXT,
			tmux_pane TEXT,
			started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
			last_activity TEXT,
			ended_at TEXT,
			UNIQUE (session_id),
			FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
			FOREIGN KEY (active_goal_id) REFERENCES plans(id) ON DELETE SET NULL
		)`)
		db.Exec(`INSERT INTO ai_sessions_new
			(id, session_id, project_id, platform, state, active_goal_id, working_dir,
			 transcript_path, plan_file_path, tmux_pane, started_at, last_activity, ended_at)
			SELECT id, session_id, project_id, platform, state, active_goal_id, working_dir,
			       transcript_path, plan_file_path, tmux_pane, started_at, last_activity, ended_at
			FROM ai_sessions`)
		db.Exec("DROP TABLE ai_sessions")
		db.Exec("ALTER TABLE ai_sessions_new RENAME TO ai_sessions")
		db.Exec("PRAGMA foreign_keys=ON")
	}

	// Add tmux_pane column to ai_sessions if missing
	rows, err = db.Query("PRAGMA table_info(ai_sessions)")
	if err == nil {
		hasTmuxPane := false
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt *string
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == "tmux_pane" {
				hasTmuxPane = true
			}
		}
		rows.Close()
		if !hasTmuxPane {
			db.Exec("ALTER TABLE ai_sessions ADD COLUMN tmux_pane TEXT")
		}
	}

	// Create msg_channels table if missing
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='msg_channels'").Scan(&count)
	if err == nil && count == 0 {
		db.Exec(`CREATE TABLE IF NOT EXISTS msg_channels (
			id INTEGER PRIMARY KEY,
			channel_id TEXT NOT NULL UNIQUE,
			session_a TEXT NOT NULL,
			pane_a TEXT NOT NULL,
			session_b TEXT,
			pane_b TEXT,
			project_id INTEGER,
			state TEXT NOT NULL DEFAULT 'beacon'
				CHECK (state IN ('beacon', 'connected', 'closed')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
			connected_at TEXT,
			closed_at TEXT,
			FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
		)`)
	}

	// Create msg_queue table if missing
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='msg_queue'").Scan(&count)
	if err == nil && count == 0 {
		db.Exec(`CREATE TABLE IF NOT EXISTS msg_queue (
			id INTEGER PRIMARY KEY,
			channel_id TEXT NOT NULL,
			sender TEXT NOT NULL,
			body TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued'
				CHECK (status IN ('queued', 'delivered')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
			delivered_at TEXT,
			FOREIGN KEY (channel_id) REFERENCES msg_channels(channel_id) ON DELETE CASCADE
		)`)
	}

	// Add description + prompt columns, migrate from task_text
	rows, err = db.Query("PRAGMA table_info(plans)")
	if err == nil {
		hasDescription := false
		hasPrompt := false
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt *string
			var pk int
			rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
			if name == "description" {
				hasDescription = true
			}
			if name == "prompt" {
				hasPrompt = true
			}
		}
		rows.Close()
		if !hasDescription {
			db.Exec("ALTER TABLE plans ADD COLUMN description TEXT")
			db.Exec("UPDATE plans SET description = task_text WHERE task_text IS NOT NULL AND description IS NULL")
		}
		if !hasPrompt {
			db.Exec("ALTER TABLE plans ADD COLUMN prompt TEXT")
		}
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
