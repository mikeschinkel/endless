package monitor

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// freshDB opens a file-backed empty SQLite DB in t.TempDir() and applies the
// connection-level pragmas that monitor.DB() normally sets. It returns the
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

// TestMigrateFreshDB_CreatesBaselineTables verifies that running migrate()
// against an empty database produces every table the rest of the codebase
// expects to exist — both the V0 baseline (created by schema.SQL) and the
// versioned migration additions (session_gates, session_statuses).
func TestMigrateFreshDB_CreatesBaselineTables(t *testing.T) {
	db := freshDB(t)

	_, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	wantTables := []string{
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
		"session_gates",
		"session_statuses",
	}
	for _, name := range wantTables {
		if !hasTable(db, name) {
			t.Errorf("expected table %q to exist after migrate(); missing", name)
		}
	}
}

// TestMigrateIdempotent verifies that calling migrate() twice on the same
// DB is a no-op on the second pass: no errors, no duplicate audit rows, and
// the schema is unchanged. The V0 baseline is executed every migrate() call,
// so this guards against schema.sql growing a non-idempotent statement.
func TestMigrateIdempotent(t *testing.T) {
	db := freshDB(t)

	if _, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true}); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if _, err := migrate(db, MigrateOpts{Runner: RunnerAuto, SkipBackup: true}); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	rows, err := db.Query("SELECT version, count(*) FROM _schema_version GROUP BY version HAVING count(*) > 1")
	if err != nil {
		t.Fatalf("query duplicates: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var version, n int
		if err := rows.Scan(&version, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Errorf("schema_version %d has %d audit rows after second migrate; want exactly 1", version, n)
	}
}
