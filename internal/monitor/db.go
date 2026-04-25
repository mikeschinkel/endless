package monitor

import (
	"database/sql"
	"fmt"
	"log"
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

// ConfigDir returns the Endless configuration directory.
func ConfigDir() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "endless")
}

// DBPath returns the path to the Endless SQLite database.
func DBPath() string {
	return filepath.Join(ConfigDir(), "endless.db")
}

// DB returns a connection to the Endless SQLite database.
func DB() (*sql.DB, error) {
	dbOnce.Do(func() {
		path := DBPath()
		dbConn, dbErr = sql.Open("sqlite", path)
		if dbErr != nil {
			dbErr = fmt.Errorf("opening database %s: %w", path, dbErr)
			return
		}
		// Verify the connection actually works (sql.Open may succeed lazily)
		if err := dbConn.Ping(); err != nil {
			dbErr = fmt.Errorf("connecting to database %s: %w", path, err)
			dbConn = nil
			return
		}
		if _, err := dbConn.Exec("PRAGMA journal_mode=WAL"); err != nil {
			log.Printf("endless-monitor: PRAGMA journal_mode=WAL: %v", err)
		}
		if _, err := dbConn.Exec("PRAGMA busy_timeout=5000"); err != nil {
			log.Printf("endless-monitor: PRAGMA busy_timeout=5000: %v", err)
		}
		if _, err := dbConn.Exec("PRAGMA foreign_keys=ON"); err != nil {
			log.Printf("endless-monitor: PRAGMA foreign_keys=ON: %v", err)
		}
		migrate(dbConn)
	})
	return dbConn, dbErr
}

func hasTable(db *sql.DB, table string) bool {
	var count int
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
	return count > 0
}

func hasColumn(db *sql.DB, table, column string) bool {
	rows, err := db.Query("PRAGMA table_info("+table+")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt *string
		var pk int
		rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
		if name == column {
			return true
		}
	}
	return false
}

// migrate runs schema migrations for existing databases.
func migrate(db *sql.DB) {
	migrateV1(db)
	migrateV2(db)
	migrateV3(db)
}

// migrateV3 adds session conversation history tables (E-857).
func migrateV3(db *sql.DB) {
	// session_messages table
	if !hasTable(db, "session_messages") {
		db.Exec(`CREATE TABLE IF NOT EXISTS session_messages (
			id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool_use')),
			content TEXT NOT NULL,
			tool_name TEXT,
			message_uuid TEXT UNIQUE,
			created_at TEXT NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
		)`)
		db.Exec(`CREATE INDEX IF NOT EXISTS idx_session_messages_session
			ON session_messages(session_id, created_at DESC)`)
	}

	// FTS5 for cross-session search
	if !hasTable(db, "session_messages_fts") {
		db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS session_messages_fts USING fts5(
			content,
			content=session_messages,
			content_rowid=id
		)`)
		db.Exec(`CREATE TRIGGER IF NOT EXISTS session_messages_ai AFTER INSERT ON session_messages BEGIN
			INSERT INTO session_messages_fts(rowid, content) VALUES (new.id, new.content);
		END`)
		db.Exec(`CREATE TRIGGER IF NOT EXISTS session_messages_ad AFTER DELETE ON session_messages BEGIN
			INSERT INTO session_messages_fts(session_messages_fts, rowid, content) VALUES('delete', old.id, old.content);
		END`)
	}

	// Add new columns to sessions
	if hasTable(db, "sessions") {
		if !hasColumn(db, "sessions", "transcript_offset") {
			db.Exec("ALTER TABLE sessions ADD COLUMN transcript_offset INTEGER NOT NULL DEFAULT 0")
		}
		if !hasColumn(db, "sessions", "transcript_path") {
			db.Exec("ALTER TABLE sessions ADD COLUMN transcript_path TEXT")
		}
		if !hasColumn(db, "sessions", "summary") {
			db.Exec("ALTER TABLE sessions ADD COLUMN summary TEXT")
		}
		if !hasColumn(db, "sessions", "hidden") {
			db.Exec("ALTER TABLE sessions ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0")
		}
	}
}

// migrateV1 handles legacy schema migrations (plans→tasks, column additions).
func migrateV1(db *sql.DB) {
	// Determine the session table name (may be ai_sessions or sessions depending on v2 state)
	sessionTable := "ai_sessions"
	if hasTable(db, "sessions") && !hasTable(db, "ai_sessions") {
		sessionTable = "sessions"
	}

	// Rename plans table to tasks if needed
	if hasTable(db, "plans") && !hasTable(db, "tasks") {
		db.Exec("ALTER TABLE plans RENAME TO tasks")
	}

	// Add type column to tasks if missing
	if hasTable(db, "tasks") {
		if !hasColumn(db, "tasks", "type") {
			db.Exec("ALTER TABLE tasks ADD COLUMN type TEXT NOT NULL DEFAULT 'task'")
		}
		if hasColumn(db, "tasks", "plan_id") && !hasColumn(db, "tasks", "task_id") {
			db.Exec("ALTER TABLE tasks RENAME COLUMN plan_id TO task_id")
		}
	}

	// Add updated_at column to tasks if missing
	if hasTable(db, "tasks") && !hasColumn(db, "tasks", "updated_at") {
		db.Exec("ALTER TABLE tasks ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''")
		db.Exec("UPDATE tasks SET updated_at = created_at WHERE updated_at = ''")
		db.Exec(`CREATE TRIGGER IF NOT EXISTS tasks_updated_at AFTER UPDATE ON tasks
			BEGIN
				UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
				WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
			END`)
	}

	// Add title column to tasks if missing
	if hasTable(db, "tasks") && !hasColumn(db, "tasks", "title") {
		db.Exec("ALTER TABLE tasks ADD COLUMN title TEXT")
		db.Exec("UPDATE tasks SET title = substr(description, 1, 80) WHERE title IS NULL")
	}

	// Add active_goal_id column to session table if missing (legacy)
	if hasTable(db, sessionTable) {
		if !hasColumn(db, sessionTable, "active_task_id") && !hasColumn(db, sessionTable, "active_goal_id") {
			db.Exec("ALTER TABLE " + sessionTable + " ADD COLUMN active_goal_id INTEGER REFERENCES tasks(id)")
		}
		if hasColumn(db, sessionTable, "active_task_id") && !hasColumn(db, sessionTable, "active_goal_id") {
			db.Exec("ALTER TABLE " + sessionTable + " ADD COLUMN active_goal_id INTEGER REFERENCES tasks(id)")
			db.Exec("UPDATE " + sessionTable + " SET active_goal_id = active_task_id WHERE active_task_id IS NOT NULL")
		}
	}

	// Add plan_file_path column to session table if missing
	if hasTable(db, sessionTable) && !hasColumn(db, sessionTable, "plan_file_path") {
		db.Exec("ALTER TABLE " + sessionTable + " ADD COLUMN plan_file_path TEXT")
	}

	// Create task_deps table if missing
	if !hasTable(db, "task_deps") {
		db.Exec(`CREATE TABLE IF NOT EXISTS task_deps (
			id INTEGER PRIMARY KEY,
			source_type TEXT NOT NULL CHECK (source_type IN ('task', 'project')),
			source_id INTEGER NOT NULL,
			target_type TEXT NOT NULL CHECK (target_type IN ('task', 'project')),
			target_id INTEGER NOT NULL,
			dep_type TEXT NOT NULL DEFAULT 'blocks' CHECK (dep_type IN ('blocks', 'needs')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
			UNIQUE(source_type, source_id, target_type, target_id)
		)`)
	}

	// Fix broken FK references: active_goal_id referencing plan_items instead of tasks
	// V1 FK fix (plan_items → tasks) — MOVED TO MANUAL MIGRATION
	// Only needed for very old DBs with plan_items FK reference.

	// Add tmux_pane column to session table if missing
	if hasTable(db, sessionTable) && !hasColumn(db, sessionTable, "tmux_pane") && !hasColumn(db, sessionTable, "process") {
		db.Exec("ALTER TABLE " + sessionTable + " ADD COLUMN tmux_pane TEXT")
	}

	// Create channels table if missing
	if !hasTable(db, "channels") {
		db.Exec(`CREATE TABLE IF NOT EXISTS channels (
			process TEXT PRIMARY KEY,
			port INTEGER NOT NULL,
			pid INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
		)`)
	}

	// Create messaging tables if missing
	if !hasTable(db, "conversations") {
		db.Exec(`CREATE TABLE IF NOT EXISTS conversations (
			id INTEGER PRIMARY KEY,
			conversation_id TEXT NOT NULL UNIQUE,
			process_a TEXT NOT NULL,
			process_b TEXT,
			project_id INTEGER,
			state TEXT NOT NULL DEFAULT 'beacon'
				CHECK (state IN ('beacon', 'connected', 'closed')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
			connected_at TEXT,
			closed_at TEXT,
			FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
		)`)
	}
	if !hasTable(db, "msg_queue") && !hasTable(db, "messages") {
		db.Exec(`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			sender TEXT NOT NULL,
			body TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued'
				CHECK (status IN ('queued', 'delivered')),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
			delivered_at TEXT,
			FOREIGN KEY (conversation_id) REFERENCES conversations(conversation_id) ON DELETE CASCADE
		)`)
	}

	// Add description + prompt columns, migrate from task_text
	if hasTable(db, "tasks") {
		if !hasColumn(db, "tasks", "description") {
			db.Exec("ALTER TABLE tasks ADD COLUMN description TEXT")
			db.Exec("UPDATE tasks SET description = task_text WHERE task_text IS NOT NULL AND description IS NULL")
		}
		if !hasColumn(db, "tasks", "prompt") {
			db.Exec("ALTER TABLE tasks ADD COLUMN prompt TEXT")
		}
	}
}

// migrateV2 handles schema v2 changes: drop dead tables, rename tables/columns,
// drop unused columns, fix CHECK constraints.
func migrateV2(db *sql.DB) {
	// === Step 1: Drop dead tables (E-741) ===
	for _, table := range []string{
		"doc_dependencies", "doc_regions", "ai_chats",
		"private_files", "privacy_rules", "claude_sessions",
		"file_changes", "scan_log", "documents",
	} {
		db.Exec("DROP TABLE IF EXISTS " + table)
	}
	// Drop old sessions table (ZSH prompt hook — NOT ai_sessions)
	// Only drop if ai_sessions still exists (meaning the old sessions is the dead one)
	if hasTable(db, "sessions") && hasTable(db, "ai_sessions") {
		db.Exec("DROP TABLE sessions")
	}

	// === Step 2: Rename tables (E-742) ===
	if hasTable(db, "msg_queue") && !hasTable(db, "messages") {
		db.Exec("ALTER TABLE msg_queue RENAME TO messages")
	}
	if hasTable(db, "msg_channels") && !hasTable(db, "conversations") {
		db.Exec("ALTER TABLE msg_channels RENAME TO conversations")
	}
	if hasTable(db, "ai_sessions") && !hasTable(db, "sessions") {
		db.Exec("ALTER TABLE ai_sessions RENAME TO sessions")
	}

	// === Step 3: Rename columns (E-743) ===
	if hasTable(db, "sessions") {
		if hasColumn(db, "sessions", "active_goal_id") && !hasColumn(db, "sessions", "active_task_id") {
			db.Exec("ALTER TABLE sessions RENAME COLUMN active_goal_id TO active_task_id")
		}
		if hasColumn(db, "sessions", "tmux_pane") && !hasColumn(db, "sessions", "process") {
			db.Exec("ALTER TABLE sessions RENAME COLUMN tmux_pane TO process")
		}
	}
	if hasTable(db, "conversations") {
		if hasColumn(db, "conversations", "channel_id") {
			db.Exec("ALTER TABLE conversations RENAME COLUMN channel_id TO conversation_id")
		}
		if hasColumn(db, "conversations", "pane_a") {
			db.Exec("ALTER TABLE conversations RENAME COLUMN pane_a TO process_a")
		}
		if hasColumn(db, "conversations", "pane_b") {
			db.Exec("ALTER TABLE conversations RENAME COLUMN pane_b TO process_b")
		}
		// Drop legacy conversations with session_a/session_b — will be recreated clean
		if hasColumn(db, "conversations", "session_a") {
			db.Exec("DROP TABLE IF EXISTS messages")
			db.Exec("DROP TABLE IF EXISTS conversations")
		}
	}
	if hasTable(db, "messages") {
		if hasColumn(db, "messages", "channel_id") {
			db.Exec("ALTER TABLE messages RENAME COLUMN channel_id TO conversation_id")
		}
	}

	// Step 4 (E-744): Drop unused session columns — MOVED TO MANUAL MIGRATION
	// Table rebuilds are destructive and caused data loss when run automatically.
	// Run 'endless db migrate' to execute rebuild migrations with backup.

	// Step 5 removed: task_dependencies → task_deps rename completed on all databases.

	// === Step 6: Fix task_deps CHECK constraints (E-745) ===
	// Step 6 (E-745): Fix task_deps CHECK constraints — MOVED TO MANUAL MIGRATION
	// UPDATE of data values is safe and kept here:
	if hasTable(db, "task_deps") {
		db.Exec("UPDATE task_deps SET source_type='task' WHERE source_type='plan'")
		db.Exec("UPDATE task_deps SET target_type='task' WHERE target_type='plan'")
	}

	// === Safety net: ensure sessions table exists ===
	// Handles edge cases where partial migrations left the table missing
	if !hasTable(db, "sessions") {
		db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL,
			project_id INTEGER,
			platform TEXT NOT NULL DEFAULT 'claude' CHECK (platform IN ('claude', 'codex')),
			state TEXT NOT NULL DEFAULT 'working' CHECK (state IN ('working', 'idle', 'needs_input', 'ended')),
			active_task_id INTEGER,
			plan_file_path TEXT,
			process TEXT,
			started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
			last_activity TEXT,
			UNIQUE (session_id),
			FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
			FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL
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
