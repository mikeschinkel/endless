package monitor

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// freshDB opens a file-backed empty SQLite DB in t.TempDir(). It returns the
// open handle; the test runner cleans up the tempdir.
func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "endless.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

// applySchema executes schema.SQL against db — the same call monitor.DB() makes
// on every connection. schema.SQL is the authoritative schema (all CREATE ...
// IF NOT EXISTS), so this creates every table on a fresh DB and is a no-op on a
// populated one. Tests use it where they previously called the deleted migrate().
func applySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
}

// withTestDB rebinds monitor.DB()'s singleton to a fresh schema-applied DB for
// the lifetime of t. Restores the previous state on cleanup so tests run
// sequentially without dbOnce leaking. Use this for any test that exercises a
// public monitor.* wrapper that calls DB() internally — TouchSession, TaskText,
// BindSessionToTask, etc. Tests must NOT t.Parallel() while the seam is held.
//
// The seam also satisfies guardWorktreeDBContext (E-1429) by setting
// dbContextDir to a non-empty path, so tests running from inside this
// self-dev worktree don't trip the gate.
func withTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := freshDB(t)
	applySchema(t, db)

	prevOnce, prevConn, prevErr := dbOnce, dbConn, dbErr
	prevCtxDir, prevPathOverride := dbContextDir, dbPathOverride

	dbOnce = &sync.Once{}
	dbOnce.Do(func() {}) // mark consumed so DB() returns dbConn directly
	dbConn = db
	dbErr = nil
	dbContextDir = t.TempDir() // satisfy the E-1429 gate

	t.Cleanup(func() {
		dbOnce = prevOnce
		dbConn = prevConn
		dbErr = prevErr
		dbContextDir = prevCtxDir
		dbPathOverride = prevPathOverride
	})
	return db
}

// seedProject inserts a row into projects with the given id/name/path. Tests
// that exercise functions whose SQL joins or FKs reference projects use this
// to satisfy foreign-key constraints. Returns the seeded id for chained calls.
func seedProject(t *testing.T, db *sql.DB, id int64, name, path string) int64 {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (?, ?, ?)",
		id, name, path,
	); err != nil {
		t.Fatalf("seed project id=%d: %v", id, err)
	}
	return id
}

// TestSchemaFreshDB_CreatesAllTables verifies that applying schema.SQL to an
// empty database produces every table the rest of the codebase expects to
// exist. With the V-migration framework gone, schema.sql is the single source
// of truth, so all of these (formerly split between schema.sql and migrateV*)
// must be present after one apply.
func TestSchemaFreshDB_CreatesAllTables(t *testing.T) {
	db := freshDB(t)
	applySchema(t, db)

	wantTables := []string{
		"_schema_version",
		"projects",
		"project_deps",
		"notes",
		"sessions",
		"tasks",
		"task_deps",
		"activity",
		"channels",
		"conversations",
		"messages",
		"session_messages",
		"task_files",
		"suggestions",
		"task_landings",
		"session_statuses",
		"session_tasks",
		"project_next",
		"project_next_lanes",
		"project_next_tasks",
		"project_next_pending",
		"project_next_events",
	}
	for _, name := range wantTables {
		if !hasTable(db, name) {
			t.Errorf("expected table %q to exist after applying schema.sql; missing", name)
		}
	}
}

// TestSchemaReexecIdempotent verifies that applying schema.SQL twice on the
// same DB is a no-op on the second pass: no errors and no duplicated objects.
// Idempotency is now a property of schema.sql itself (every statement is
// IF NOT EXISTS), which monitor.DB() relies on running schema.SQL per connect.
func TestSchemaReexecIdempotent(t *testing.T) {
	db := freshDB(t)
	applySchema(t, db) // applySchema t.Fatalf's on any error...
	applySchema(t, db) // ...so a second clean apply is the idempotency assertion.

	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='tasks'",
	).Scan(&n); err != nil {
		t.Fatalf("count tasks table: %v", err)
	}
	if n != 1 {
		t.Errorf("tasks table defined %d times after re-exec; want 1", n)
	}
}
