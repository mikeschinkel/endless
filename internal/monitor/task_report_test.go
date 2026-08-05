package monitor

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// reportTestDB opens an in-memory DB with the schema applied and one project.
func reportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

func seedReportTask(t *testing.T, db *sql.DB, id int64, status string, parent *int64) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, parent_id) VALUES (?, 1, ?, ?, ?)",
		id, "t", status, parent,
	); err != nil {
		t.Fatalf("seed task E-%d: %v", id, err)
	}
}

// seedReportTaskType sets a seeded task's type slug by resolving it through
// task_types, so the tests bind to the slug the renderer gates on rather than to
// a hardcoded id.
func seedReportTaskType(t *testing.T, db *sql.DB, id int64, slug string) {
	t.Helper()
	if _, err := db.Exec(
		"UPDATE tasks SET type_id = (SELECT id FROM task_types WHERE slug = ?) WHERE id = ?",
		slug, id,
	); err != nil {
		t.Fatalf("set type %q on E-%d: %v", slug, id, err)
	}
}

func seedDep(t *testing.T, db *sql.DB, sourceID, targetID int64, depType string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', ?, 'task', ?, ?)`,
		sourceID, targetID, depType,
	); err != nil {
		t.Fatalf("seed dep %d->%d %s: %v", sourceID, targetID, depType, err)
	}
}

// TestTaskReportFacts_StatusAndClean pins the common path: a lone task with no
// successors, children, or landing yields its status and empty collections.
func TestTaskReportFacts_StatusAndClean(t *testing.T) {
	db := reportTestDB(t)
	seedReportTask(t, db, 100, "unverified", nil)

	facts, err := taskReportFacts(db, 100)
	if err != nil {
		t.Fatalf("taskReportFacts: %v", err)
	}
	if facts.Status != "unverified" {
		t.Errorf("status = %q, want unverified", facts.Status)
	}
	if facts.Landed {
		t.Errorf("landed = true, want false")
	}
	if len(facts.Successors) != 0 {
		t.Errorf("successors = %v, want none", facts.Successors)
	}
	if len(facts.Children) != 0 {
		t.Errorf("children = %v, want none", facts.Children)
	}
}

// TestTaskReportFacts_Successors covers both successor directions: a task this
// task blocks, and a task that cleans up this task. Each carries its status and
// the relation seen from the focal task.
func TestTaskReportFacts_Successors(t *testing.T) {
	db := reportTestDB(t)
	seedReportTask(t, db, 100, "unverified", nil)
	seedReportTask(t, db, 200, "submitted", nil)  // 100 blocks 200
	seedReportTask(t, db, 300, "unplanned", nil)   // 300 cleans_up 100
	seedReportTask(t, db, 400, "ready", nil)       // 100 cleans_up 400 -> upstream, NOT a successor
	seedDep(t, db, 100, 200, "blocks")
	seedDep(t, db, 300, 100, "cleans_up")
	seedDep(t, db, 100, 400, "cleans_up")

	facts, err := taskReportFacts(db, 100)
	if err != nil {
		t.Fatalf("taskReportFacts: %v", err)
	}
	got := map[int64]TaskRef{}
	for _, r := range facts.Successors {
		got[r.ID] = r
	}
	if len(got) != 2 {
		t.Fatalf("successors = %v, want exactly E-200 and E-300", facts.Successors)
	}
	if r := got[200]; r.Relation != "blocks" || r.Status != "submitted" {
		t.Errorf("E-200 = %+v, want relation=blocks status=submitted", r)
	}
	if r := got[300]; r.Relation != "cleaned_up_by" || r.Status != "unplanned" {
		t.Errorf("E-300 = %+v, want relation=cleaned_up_by status=unplanned", r)
	}
	if _, ok := got[400]; ok {
		t.Errorf("E-400 (upstream cleans_up target) must not be a successor")
	}
}

// TestTaskReportFacts_Type reports the focal task's type slug, and "" when the
// row is untyped. The renderer gates the Children list on this (E-1880), so an
// untyped row must read as "not an epic" rather than error.
func TestTaskReportFacts_Type(t *testing.T) {
	db := reportTestDB(t)
	seedReportTask(t, db, 100, "unverified", nil)
	seedReportTaskType(t, db, 100, "epic")
	seedReportTask(t, db, 200, "unverified", nil) // left untyped

	facts, err := taskReportFacts(db, 100)
	if err != nil {
		t.Fatalf("taskReportFacts: %v", err)
	}
	if facts.Type != "epic" {
		t.Errorf("type = %q, want epic", facts.Type)
	}

	facts, err = taskReportFacts(db, 200)
	if err != nil {
		t.Fatalf("taskReportFacts (untyped): %v", err)
	}
	if facts.Type != "" {
		t.Errorf("untyped task type = %q, want empty", facts.Type)
	}
}

// TestTaskReportFacts_Children returns direct children with status. Any type can
// have them — the epic-only gate is the renderer's, not this query's.
func TestTaskReportFacts_Children(t *testing.T) {
	db := reportTestDB(t)
	epic := int64(500)
	seedReportTask(t, db, epic, "underway", nil)
	seedReportTask(t, db, 501, "assumed", &epic)
	seedReportTask(t, db, 502, "unplanned", &epic)

	facts, err := taskReportFacts(db, epic)
	if err != nil {
		t.Fatalf("taskReportFacts: %v", err)
	}
	if len(facts.Children) != 2 {
		t.Fatalf("children = %v, want E-501 and E-502", facts.Children)
	}
	status := map[int64]string{}
	for _, r := range facts.Children {
		status[r.ID] = r.Status
	}
	if status[501] != "assumed" || status[502] != "unplanned" {
		t.Errorf("children statuses = %v, want 501:assumed 502:unplanned", status)
	}
}

// TestTaskReportFacts_Landed flips when a task_landings row exists.
func TestTaskReportFacts_Landed(t *testing.T) {
	db := reportTestDB(t)
	seedReportTask(t, db, 100, "confirmed", nil)
	if _, err := db.Exec(
		"INSERT INTO task_landings (task_id, merge_commit_sha) VALUES (100, 'abc123')",
	); err != nil {
		t.Fatalf("seed landing: %v", err)
	}
	facts, err := taskReportFacts(db, 100)
	if err != nil {
		t.Fatalf("taskReportFacts: %v", err)
	}
	if !facts.Landed {
		t.Errorf("landed = false, want true")
	}
}

// TestTaskReportFacts_MissingTask is a caller error, not an empty report.
func TestTaskReportFacts_MissingTask(t *testing.T) {
	db := reportTestDB(t)
	if _, err := taskReportFacts(db, 9999); err == nil {
		t.Fatal("expected an error for a missing task, got nil")
	}
}
