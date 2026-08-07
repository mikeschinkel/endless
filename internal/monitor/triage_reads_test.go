package monitor

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// triageTestDB opens an in-memory DB with the schema applied and two projects,
// so the project filter and the ledger-wide sweep are both exercisable.
func triageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err = db.Exec(
		`INSERT INTO projects (id, name, path) VALUES
		   (1, 'alpha', '/tmp/alpha'),
		   (2, 'beta',  '/tmp/beta')`,
	); err != nil {
		t.Fatalf("seed projects: %v", err)
	}
	return db
}

// seedTriageTask inserts one task. createdAt drives the queue's oldest-first
// ordering, so it is explicit rather than defaulted — the default is
// second-granularity now(), which would make every seeded row tie.
func seedTriageTask(
	t *testing.T,
	db *sql.DB,
	id, projectID int64,
	title, status, createdAt string,
	parent *int64,
) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, created_at, parent_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, projectID, title, status, createdAt, parent,
	); err != nil {
		t.Fatalf("seed task E-%d: %v", id, err)
	}
}

func TestUntriagedTasks_OldestFirstAndCapped(t *testing.T) {
	db := triageTestDB(t)
	seedTriageTask(t, db, 10, 1, "newest", "untriaged", "2026-08-03T00:00:00", nil)
	seedTriageTask(t, db, 11, 1, "oldest", "untriaged", "2026-08-01T00:00:00", nil)
	seedTriageTask(t, db, 12, 1, "middle", "untriaged", "2026-08-02T00:00:00", nil)
	// Not untriaged — a triaged task must never be re-selected, which is what
	// makes a re-claimed sweep idempotent.
	seedTriageTask(t, db, 13, 1, "already", "submitted", "2026-07-01T00:00:00", nil)
	// Tier-1 filings land straight in `ready` and are exempt from triage.
	seedTriageTask(t, db, 14, 1, "tier one", "ready", "2026-07-02T00:00:00", nil)

	got, err := untriagedTasks(db, "", 2)
	if err != nil {
		t.Fatalf("untriagedTasks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("limit not applied: got %d rows, want 2", len(got))
	}
	if got[0].ID != 11 || got[1].ID != 12 {
		t.Errorf("wrong order: got %d,%d want 11,12", got[0].ID, got[1].ID)
	}
	if got[0].Project != "alpha" {
		t.Errorf("project name: got %q want %q", got[0].Project, "alpha")
	}
	if got[0].Title != "oldest" {
		t.Errorf("title: got %q want %q", got[0].Title, "oldest")
	}
}

// Same created_at is ordinary — a script filing three follow-ups lands them in
// one second — so id must break the tie deterministically.
func TestUntriagedTasks_TieBrokenByID(t *testing.T) {
	db := triageTestDB(t)
	seedTriageTask(t, db, 30, 1, "c", "untriaged", "2026-08-01T00:00:00", nil)
	seedTriageTask(t, db, 28, 1, "a", "untriaged", "2026-08-01T00:00:00", nil)
	seedTriageTask(t, db, 29, 1, "b", "untriaged", "2026-08-01T00:00:00", nil)

	got, err := untriagedTasks(db, "", 10)
	if err != nil {
		t.Fatalf("untriagedTasks: %v", err)
	}
	want := []int64{28, 29, 30}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("tie order: got %v want %v", got, want)
		}
	}
}

func TestUntriagedTasks_ProjectFilter(t *testing.T) {
	db := triageTestDB(t)
	seedTriageTask(t, db, 40, 1, "in alpha", "untriaged", "2026-08-01T00:00:00", nil)
	seedTriageTask(t, db, 41, 2, "in beta", "untriaged", "2026-08-01T00:00:00", nil)

	all, err := untriagedTasks(db, "", 10)
	if err != nil {
		t.Fatalf("untriagedTasks(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ledger-wide sweep: got %d want 2", len(all))
	}

	only, err := untriagedTasks(db, "beta", 10)
	if err != nil {
		t.Fatalf("untriagedTasks(beta): %v", err)
	}
	if len(only) != 1 || only[0].ID != 41 {
		t.Fatalf("project filter: got %v want just E-41", only)
	}
}

func TestUntriagedTasks_EmptyQueueIsEmptySlice(t *testing.T) {
	db := triageTestDB(t)
	got, err := untriagedTasks(db, "", 10)
	if err != nil {
		t.Fatalf("untriagedTasks: %v", err)
	}
	// Not nil: the JSON wire contract is `[]`, and the Python caller iterates
	// it without a None check.
	if got == nil {
		t.Fatal("empty queue returned nil, want an empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("empty queue returned %d rows", len(got))
	}
}

func TestTriageContext_RootTaskHasNoParentOrSiblings(t *testing.T) {
	db := triageTestDB(t)
	seedTriageTask(t, db, 50, 1, "a root task", "untriaged", "2026-08-01T00:00:00", nil)
	// Another root task in the same project. It is NOT a sibling: a root
	// task's "siblings" would be every root task in the project, which says
	// nothing about this task and is unbounded.
	seedTriageTask(t, db, 51, 1, "another root", "untriaged", "2026-08-01T00:00:00", nil)

	ctx, err := triageContext(db, 50)
	if err != nil {
		t.Fatalf("triageContext: %v", err)
	}
	if ctx.Parent != nil {
		t.Errorf("root task got a parent: %+v", ctx.Parent)
	}
	if len(ctx.Siblings) != 0 {
		t.Errorf("root task got siblings: %v", ctx.Siblings)
	}
	if ctx.Project != "alpha" || ctx.ProjectRoot != "/tmp/alpha" {
		t.Errorf("project: got %q/%q want alpha//tmp/alpha", ctx.Project, ctx.ProjectRoot)
	}
	if ctx.Status != "untriaged" {
		t.Errorf("status: got %q want untriaged", ctx.Status)
	}
	if ctx.HasText {
		t.Error("has_text true for a task with no plan")
	}
}

func TestTriageContext_ParentAndSiblings(t *testing.T) {
	db := triageTestDB(t)
	parent := int64(60)
	seedTriageTask(t, db, 60, 1, "the epic", "underway", "2026-08-01T00:00:00", nil)
	if _, err := db.Exec(
		"UPDATE tasks SET description = ? WHERE id = 60", "the umbrella",
	); err != nil {
		t.Fatalf("set parent description: %v", err)
	}
	seedTriageTask(t, db, 61, 1, "the focal child", "untriaged", "2026-08-02T00:00:00", &parent)
	seedTriageTask(t, db, 62, 1, "sibling one", "ready", "2026-08-02T00:00:00", &parent)
	seedTriageTask(t, db, 63, 1, "sibling two", "confirmed", "2026-08-02T00:00:00", &parent)

	ctx, err := triageContext(db, 61)
	if err != nil {
		t.Fatalf("triageContext: %v", err)
	}
	if ctx.Parent == nil {
		t.Fatal("no parent")
	}
	if ctx.Parent.ID != 60 || ctx.Parent.Title != "the epic" {
		t.Errorf("parent: got %+v", ctx.Parent)
	}
	if ctx.Parent.Description != "the umbrella" {
		t.Errorf("parent description: got %q", ctx.Parent.Description)
	}
	// The focal task must not appear in its own sibling list.
	if strings.Join(ctx.Siblings, ",") != "sibling one,sibling two" {
		t.Errorf("siblings: got %v", ctx.Siblings)
	}
}

// E-1378 split decision relations by source table. Reading only one would
// silently drop half the decisions a task is constrained by, so both are
// asserted together.
func TestTriageContext_DecisionsFromBothRelationTables(t *testing.T) {
	db := triageTestDB(t)
	seedTriageTask(t, db, 70, 1, "the focal task", "untriaged", "2026-08-01T00:00:00", nil)
	if _, err := db.Exec(
		`INSERT INTO decisions (id, project_id, title, status) VALUES
		   (1, 1, 'Use one resolver', 'accepted'),
		   (2, 1, 'Fail open always', 'proposed')`,
	); err != nil {
		t.Fatalf("seed decisions: %v", err)
	}
	// decision -> task (a decision that documents the task).
	if _, err := db.Exec(
		`INSERT INTO decision_relations
		   (source_decision_id, target_kind, target_id, relation_type)
		 VALUES (1, 'task', 70, 'documents')`,
	); err != nil {
		t.Fatalf("seed decision_relation: %v", err)
	}
	// task -> decision (still in task_deps).
	if _, err := db.Exec(
		`INSERT INTO task_deps
		   (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', 70, 'decision', 2, 'implements')`,
	); err != nil {
		t.Fatalf("seed task_dep: %v", err)
	}

	ctx, err := triageContext(db, 70)
	if err != nil {
		t.Fatalf("triageContext: %v", err)
	}
	if len(ctx.Decisions) != 2 {
		t.Fatalf("decisions: got %+v want both tables represented", ctx.Decisions)
	}
	byID := map[int64]TriageDecision{}
	for _, d := range ctx.Decisions {
		byID[d.ID] = d
	}
	if byID[1].Relation != "documents" || byID[1].Title != "Use one resolver" {
		t.Errorf("decision_relations row: got %+v", byID[1])
	}
	if byID[2].Relation != "implements" || byID[2].Status != "proposed" {
		t.Errorf("task_deps row: got %+v", byID[2])
	}
}

func TestTriageContext_MissingTaskIsAnError(t *testing.T) {
	db := triageTestDB(t)
	if _, err := triageContext(db, 999); err == nil {
		t.Fatal("missing task returned no error; triage would prompt about nothing")
	}
}

func TestTriageContext_HasTextReportsAnAttachedPlan(t *testing.T) {
	db := triageTestDB(t)
	seedTriageTask(t, db, 80, 1, "planned already", "untriaged", "2026-08-01T00:00:00", nil)
	if _, err := db.Exec("UPDATE tasks SET text = ? WHERE id = 80", "# Plan\n"); err != nil {
		t.Fatalf("attach plan: %v", err)
	}
	ctx, err := triageContext(db, 80)
	if err != nil {
		t.Fatalf("triageContext: %v", err)
	}
	if !ctx.HasText {
		t.Error("has_text false for a task with an attached plan")
	}
}
