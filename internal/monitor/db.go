package monitor

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mikeschinkel/endless/internal/schema"
)

var (
	dbOnce sync.Once
	dbConn *sql.DB
	dbErr  error

	// dbPathOverride, when set, forces DBPath() to a fixed location regardless
	// of XDG_CONFIG_HOME routing. Set once by ForceRealDB() at process entry.
	dbPathOverride string
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

// CacheDir returns the Endless cache directory.
func CacheDir() string {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "endless")
}

// IsSandboxActive reports whether the current process is reading/writing
// through an E-1281 per-worktree sandbox. Detection: ConfigDir() resolves
// under CacheDir()/sandboxes/. ForceRealDB() uses this to decide whether
// hook-fired DB writes must be redirected to the real database. (Originally
// added for the plan-snapshot sandbox-skip removed in E-1449; reintroduced
// here as E-1362's ledger entry anticipated it might be "useful elsewhere".)
// See E-1450.
func IsSandboxActive() bool {
	sandboxRoot := filepath.Join(CacheDir(), "sandboxes")
	rel, err := filepath.Rel(sandboxRoot, ConfigDir())
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// DBPath returns the path to the Endless SQLite database.
func DBPath() string {
	if dbPathOverride != "" {
		return dbPathOverride
	}
	return filepath.Join(ConfigDir(), "endless.db")
}

// ForceRealDB routes monitor.DB() and DBPath()-derived artifacts (e.g. backups)
// to the real database under ~/.config/endless, ignoring the E-1281 sandbox
// XDG_CONFIG_HOME routing. It overrides only the DB path: log files and global
// config.json reads keep following ConfigDir(), and because XDG_CONFIG_HOME is
// never mutated, IsSandboxActive() still reports true for any other
// sandbox-aware behavior. The endless-hook binary calls this at startup so
// hook-fired writes (session registration, activity, state transitions) reflect
// real-world activity and land in the real DB rather than throwaway sandbox
// fixtures. No-op when not sandbox-routed; must be called before the first
// DB()/DBPath() use. See E-1450.
func ForceRealDB() {
	if !IsSandboxActive() {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dbPathOverride = filepath.Join(home, ".config", "endless", "endless.db")
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
		// SQLite is single-writer; one connection ensures BEGIN IMMEDIATE
		// works correctly with Go's connection pool.
		dbConn.SetMaxOpenConns(1)
		if _, err := dbConn.Exec("PRAGMA journal_mode=WAL"); err != nil {
			log.Printf("endless-monitor: PRAGMA journal_mode=WAL: %v", err)
		}
		if _, err := dbConn.Exec("PRAGMA busy_timeout=5000"); err != nil {
			log.Printf("endless-monitor: PRAGMA busy_timeout=5000: %v", err)
		}
		if _, err := dbConn.Exec("PRAGMA foreign_keys=ON"); err != nil {
			log.Printf("endless-monitor: PRAGMA foreign_keys=ON: %v", err)
		}
		// schema.SQL is the authoritative schema, all CREATE ... IF NOT EXISTS:
		// it creates every table on a fresh DB and is a no-op on a populated
		// one. Destructive, one-off changes are applied separately at land
		// time via `endless db apply-change`, not here.
		if _, err := dbConn.Exec(schema.SQL); err != nil {
			dbErr = fmt.Errorf("applying schema to %s: %w", path, err)
			dbConn = nil
			return
		}
	})
	return dbConn, dbErr
}

func hasTable(db *sql.DB, table string) bool {
	var count int
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
	return count > 0
}

// BackupDB copies the database file to the backups directory if the last
// backup is older than 60 seconds. Keeps the last 60 backups.
func BackupDB() {
	src := DBPath()
	if _, err := os.Stat(src); err != nil {
		return
	}

	// Backups follow the DB: when ForceRealDB() has redirected DBPath() to the
	// real database, its backups land beside it rather than in the sandbox
	// (E-1450). In the normal case DBPath() is ConfigDir()/endless.db, so this
	// resolves to ConfigDir()/backups exactly as before.
	backupDir := filepath.Join(filepath.Dir(DBPath()), "backups")
	os.MkdirAll(backupDir, 0755)

	// Check if backup is needed (last backup > 60s ago)
	entries, _ := os.ReadDir(backupDir)
	if len(entries) > 0 {
		newest := entries[len(entries)-1]
		info, err := newest.Info()
		if err == nil && time.Since(info.ModTime()) < 60*time.Second {
			return // recent backup exists
		}
	}

	// Use SQLite VACUUM INTO for a consistent backup
	ts := time.Now().Format("20060102-150405")
	dst := filepath.Join(backupDir, fmt.Sprintf("endless-%s.db", ts))

	backupDB, err := sql.Open("sqlite", src)
	if err != nil {
		return
	}
	defer backupDB.Close()

	_, err = backupDB.Exec("VACUUM INTO ?", dst)
	if err != nil {
		log.Printf("backup failed: %v", err)
		return
	}

	// Rotate: keep last 60 backups
	entries, _ = os.ReadDir(backupDir)
	if len(entries) > 60 {
		for _, e := range entries[:len(entries)-60] {
			os.Remove(filepath.Join(backupDir, e.Name()))
		}
	}
}

// ProjectPath returns the registered filesystem path for a project ID.
// Path is returned with ~ expansion applied so callers can use it directly.
func ProjectPath(id int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var path string
	err = db.QueryRow("SELECT path FROM projects WHERE id = ?", id).Scan(&path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return path, nil
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

	// No registered project found — auto-register as active
	id, err := ensureAutoRegisteredProject(db, dir)
	if err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// ensureAutoRegisteredProject auto-registers an unregistered directory
// as an active project. Uses the directory basename as the project name.
func ensureAutoRegisteredProject(db *sql.DB, dir string) (int64, error) {
	// Check if already exists at this path
	var id int64
	err := db.QueryRow(
		"SELECT id FROM projects WHERE path = ?",
		dir,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	// Auto-register with directory basename as name
	name := filepath.Base(dir)
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
		name = fmt.Sprintf("%s-%d", base, i)
	}

	result, err := db.Exec(
		"INSERT INTO projects (name, path, status, created_at, updated_at) "+
			"VALUES (?, ?, 'active', ?, ?)",
		name, dir, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("auto-registering project %s at %s: %w", name, dir, err)
	}

	log.Printf("auto-registered project: %s at %s", name, dir)
	return result.LastInsertId()
}
