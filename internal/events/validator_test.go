// Black-box tests for ValidateTasks. ValidateTasks compares the tasks
// table between a "current" *sql.DB handle and a "projected" DB at a path,
// reporting field mismatches and missing tasks. Tests stand up two
// schema-applied SQLite DBs in t.TempDir() with deterministic rows.
package events_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/schema"
)

// freshTaskDB stands up a schema-applied SQLite DB in dir, seeds a "test"
// project, and returns the handle.
func freshTaskDB(t *testing.T, dir string, dbName string) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(dir, dbName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", dbName, err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema to %s: %v", dbName, err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-05-30T00:00:00', '2026-05-30T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project in %s: %v", dbName, err)
	}
	return db, path
}

// seedSimpleTask inserts a minimal task row.
func seedSimpleTask(t *testing.T, db *sql.DB, id int64, title, status string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, phase, status, type)
		 VALUES (?, 1, ?, 'now', ?, 'task')`,
		id, title, status,
	); err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}

// TestValidateTasks_IdenticalDBsReportNothing verifies that when both DBs
// hold the same row, ValidateTasks returns no mismatches and no missing
// tasks.
func TestValidateTasks_IdenticalDBsReportNothing(t *testing.T) {
	dir := t.TempDir()
	curDB, _ := freshTaskDB(t, dir, "current.db")
	projDB, projPath := freshTaskDB(t, dir, "projected.db")

	seedSimpleTask(t, curDB, 1, "Identical", "ready")
	seedSimpleTask(t, projDB, 1, "Identical", "ready")

	result, err := events.ValidateTasks(curDB, projPath)
	if err != nil {
		t.Fatalf("ValidateTasks: %v", err)
	}
	if result.TasksCompared != 1 {
		t.Errorf("TasksCompared = %d, want 1", result.TasksCompared)
	}
	if len(result.Mismatches) != 0 {
		t.Errorf("Mismatches = %v, want empty", result.Mismatches)
	}
	if len(result.MissingTasks) != 0 {
		t.Errorf("MissingTasks = %v, want empty", result.MissingTasks)
	}
}

// TestValidateTasks_FieldMismatchReported verifies that when the same id
// has a different title between current and projected, the diff appears
// in result.Mismatches with the correct field name.
func TestValidateTasks_FieldMismatchReported(t *testing.T) {
	dir := t.TempDir()
	curDB, _ := freshTaskDB(t, dir, "current.db")
	projDB, projPath := freshTaskDB(t, dir, "projected.db")

	seedSimpleTask(t, curDB, 1, "Current title", "ready")
	seedSimpleTask(t, projDB, 1, "Projected title", "ready")

	result, err := events.ValidateTasks(curDB, projPath)
	if err != nil {
		t.Fatalf("ValidateTasks: %v", err)
	}
	if len(result.Mismatches) != 1 {
		t.Fatalf("Mismatches len = %d, want 1: %v", len(result.Mismatches), result.Mismatches)
	}
	m := result.Mismatches[0]
	if m.TaskID != 1 {
		t.Errorf("Mismatches[0].TaskID = %d, want 1", m.TaskID)
	}
	if m.Field != "title" {
		t.Errorf("Mismatches[0].Field = %q, want title", m.Field)
	}
	if m.Current != "Current title" || m.Projected != "Projected title" {
		t.Errorf("Mismatches[0] values: current=%q projected=%q",
			m.Current, m.Projected)
	}
}

// TestValidateTasks_TaskOnlyInProjectedReportedMissing verifies that a
// task present in the projected DB but missing from the current DB shows
// up in result.MissingTasks (validator does not report the reverse, by
// design — see validator.go).
func TestValidateTasks_TaskOnlyInProjectedReportedMissing(t *testing.T) {
	dir := t.TempDir()
	curDB, _ := freshTaskDB(t, dir, "current.db")
	projDB, projPath := freshTaskDB(t, dir, "projected.db")

	// Only seed projected, not current.
	seedSimpleTask(t, projDB, 99, "Only in projected", "ready")

	result, err := events.ValidateTasks(curDB, projPath)
	if err != nil {
		t.Fatalf("ValidateTasks: %v", err)
	}
	if len(result.MissingTasks) != 1 {
		t.Fatalf("MissingTasks len = %d, want 1: %v", len(result.MissingTasks), result.MissingTasks)
	}
	mt := result.MissingTasks[0]
	if mt.TaskID != 99 {
		t.Errorf("MissingTasks[0].TaskID = %d, want 99", mt.TaskID)
	}
	if mt.In != "projected" {
		t.Errorf("MissingTasks[0].In = %q, want 'projected'", mt.In)
	}
}
