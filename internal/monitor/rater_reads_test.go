package monitor

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// raterTestDB opens an in-memory DB with the schema applied and two projects,
// so the project filter and the database-wide sweep are both exercisable.
func raterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err = schema.Migrate(db); err != nil {
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

// seedRaterTask inserts one task. createdAt drives the queue's oldest-first
// ordering, so it is explicit rather than defaulted — the default is
// second-granularity now(), which would make every seeded row tie.
func seedRaterTask(
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

func TestUnratedSubmittedTasks_OldestFirstAndCapped(t *testing.T) {
	db := raterTestDB(t)
	seedRaterTask(t, db, 10, 1, "newest", "submitted", "2026-08-03T00:00:00", nil)
	seedRaterTask(t, db, 11, 1, "oldest", "submitted", "2026-08-01T00:00:00", nil)
	seedRaterTask(t, db, 12, 1, "middle", "submitted", "2026-08-02T00:00:00", nil)
	// Fully rated — a rated task must never be re-selected, which is what
	// makes a re-claimed sweep idempotent.
	seedRaterTask(t, db, 13, 1, "already", "submitted", "2026-07-01T00:00:00", nil)
	if _, err := db.Exec(`UPDATE tasks SET complexity_id = 1, risk_id = 1 WHERE id = 13`); err != nil {
		t.Fatalf("rate E-13: %v", err)
	}
	// Not submitted — an unrated task anywhere else is not the rater's.
	seedRaterTask(t, db, 14, 1, "unplanned", "unplanned", "2026-07-02T00:00:00", nil)
	seedRaterTask(t, db, 15, 1, "underway", "underway", "2026-07-03T00:00:00", nil)

	got, err := unratedSubmittedTasks(db, "", 2)
	if err != nil {
		t.Fatalf("unratedSubmittedTasks: %v", err)
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
func TestUnratedSubmittedTasks_TieBrokenByID(t *testing.T) {
	db := raterTestDB(t)
	seedRaterTask(t, db, 30, 1, "c", "submitted", "2026-08-01T00:00:00", nil)
	seedRaterTask(t, db, 28, 1, "a", "submitted", "2026-08-01T00:00:00", nil)
	seedRaterTask(t, db, 29, 1, "b", "submitted", "2026-08-01T00:00:00", nil)

	got, err := unratedSubmittedTasks(db, "", 10)
	if err != nil {
		t.Fatalf("unratedSubmittedTasks: %v", err)
	}
	want := []int64{28, 29, 30}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("tie order: got %v want %v", got, want)
		}
	}
}

func TestUnratedSubmittedTasks_ProjectFilter(t *testing.T) {
	db := raterTestDB(t)
	seedRaterTask(t, db, 40, 1, "in alpha", "submitted", "2026-08-01T00:00:00", nil)
	seedRaterTask(t, db, 41, 2, "in beta", "submitted", "2026-08-01T00:00:00", nil)

	all, err := unratedSubmittedTasks(db, "", 10)
	if err != nil {
		t.Fatalf("unratedSubmittedTasks(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("database-wide sweep: got %d want 2", len(all))
	}

	only, err := unratedSubmittedTasks(db, "beta", 10)
	if err != nil {
		t.Fatalf("unratedSubmittedTasks(beta): %v", err)
	}
	if len(only) != 1 || only[0].ID != 41 {
		t.Fatalf("project filter: got %v want just E-41", only)
	}
}

func TestUnratedSubmittedTasks_EmptyQueueIsEmptySlice(t *testing.T) {
	db := raterTestDB(t)
	got, err := unratedSubmittedTasks(db, "", 10)
	if err != nil {
		t.Fatalf("unratedSubmittedTasks: %v", err)
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

func TestRaterContext_RootTaskHasNoParentOrSiblings(t *testing.T) {
	db := raterTestDB(t)
	seedRaterTask(t, db, 50, 1, "a root task", "submitted", "2026-08-01T00:00:00", nil)
	// Another root task in the same project. It is NOT a sibling: a root
	// task's "siblings" would be every root task in the project, which says
	// nothing about this task and is unbounded.
	seedRaterTask(t, db, 51, 1, "another root", "submitted", "2026-08-01T00:00:00", nil)

	ctx, err := raterContext(db, 50)
	if err != nil {
		t.Fatalf("raterContext: %v", err)
	}
	if ctx.Parent != nil {
		t.Errorf("root task got a parent: %+v", ctx.Parent)
	}
	if len(ctx.Siblings) != 0 {
		t.Errorf("root task got siblings: %v", ctx.Siblings)
	}
	// ProjectRoot comes back RESOLVED, not as stored (E-2011). /tmp is a
	// symlink into /private on macOS, so this is deliberately compared against
	// the accessor rather than the seeded literal.
	wantRoot, err := ResolvedProjectPath("/tmp/alpha")
	if err != nil {
		t.Fatalf("ResolvedProjectPath: %v", err)
	}
	if ctx.Project != "alpha" || ctx.ProjectRoot != wantRoot {
		t.Errorf("project: got %q/%q want alpha/%s", ctx.Project, ctx.ProjectRoot, wantRoot)
	}
	if ctx.Status != "submitted" {
		t.Errorf("status: got %q want submitted", ctx.Status)
	}
	if ctx.Plan != "" || ctx.Context != "" {
		t.Errorf("plan/context = %q/%q for a task with neither", ctx.Plan, ctx.Context)
	}
}

// TestRaterContext_ProjectRootIsResolved is the E-2011 regression the triage
// reads met first. This value goes straight to `endless-go event
// --project-root`, which uses it as a git work tree and as the parent of
// `.endless/db-ledger/`. Carried verbatim, every run on a project under $HOME
// would die with `project root "~/Projects/endless" is not a git work tree` —
// the ledger write failing, not the model call.
func TestRaterContext_ProjectRootIsResolved(t *testing.T) {
	db := raterTestDB(t)
	home := withTempHome(t)
	root := filepath.Join(home, "Projects", "acme")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (9, 'acme', '~/Projects/acme')",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedRaterTask(t, db, 90, 9, "a task", "submitted", "2026-08-01T00:00:00", nil)

	ctx, err := raterContext(db, 90)
	if err != nil {
		t.Fatalf("raterContext: %v", err)
	}
	if ctx.ProjectRoot != root {
		t.Errorf("ProjectRoot = %q, want %q (resolved, not the stored tilde)",
			ctx.ProjectRoot, root)
	}
}

func TestRaterContext_ParentAndSiblings(t *testing.T) {
	db := raterTestDB(t)
	parent := int64(60)
	seedRaterTask(t, db, 60, 1, "the epic", "underway", "2026-08-01T00:00:00", nil)
	if _, err := db.Exec(
		"UPDATE tasks SET description = ? WHERE id = 60", "the umbrella",
	); err != nil {
		t.Fatalf("set parent description: %v", err)
	}
	seedRaterTask(t, db, 61, 1, "the focal child", "submitted", "2026-08-02T00:00:00", &parent)
	seedRaterTask(t, db, 62, 1, "sibling one", "ready", "2026-08-02T00:00:00", &parent)
	seedRaterTask(t, db, 63, 1, "sibling two", "confirmed", "2026-08-02T00:00:00", &parent)

	ctx, err := raterContext(db, 61)
	if err != nil {
		t.Fatalf("raterContext: %v", err)
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
func TestRaterContext_DecisionsFromBothRelationTables(t *testing.T) {
	db := raterTestDB(t)
	seedRaterTask(t, db, 70, 1, "the focal task", "submitted", "2026-08-01T00:00:00", nil)
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

	ctx, err := raterContext(db, 70)
	if err != nil {
		t.Fatalf("raterContext: %v", err)
	}
	if len(ctx.Decisions) != 2 {
		t.Fatalf("decisions: got %+v want both tables represented", ctx.Decisions)
	}
	byID := map[int64]RaterDecision{}
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

func TestRaterContext_MissingTaskIsAnError(t *testing.T) {
	db := raterTestDB(t)
	if _, err := raterContext(db, 999); err == nil {
		t.Fatal("missing task returned no error; the rater would prompt about nothing")
	}
}

// The plan is what the rater judges, and the context says why the task exists,
// so both ride along in full rather than as a has-plan boolean.
func TestRaterContext_CarriesPlanAndContext(t *testing.T) {
	db := raterTestDB(t)
	seedRaterTask(t, db, 80, 1, "planned", "submitted", "2026-08-01T00:00:00", nil)
	setTaskContent(t, db, 80, "plan", "# Plan\n\nRename one flag.\n")
	setTaskContent(t, db, 80, "context", "Users trip over it.")
	ctx, err := raterContext(db, 80)
	if err != nil {
		t.Fatalf("raterContext: %v", err)
	}
	if ctx.Plan != "# Plan\n\nRename one flag.\n" {
		t.Errorf("plan: got %q", ctx.Plan)
	}
	if ctx.Context != "Users trip over it." {
		t.Errorf("context: got %q", ctx.Context)
	}
}

// TestRaterContext_CarriesCurrentRatings: the rater writes only an axis still
// unrated, so the context must say which are set.
func TestRaterContext_CarriesCurrentRatings(t *testing.T) {
	db := raterTestDB(t)
	seedRaterTask(t, db, 81, 1, "rated at filing", "submitted", "2026-08-01T00:00:00", nil)
	if _, err := db.Exec(`UPDATE tasks SET risk_id = 5 WHERE id = 81`); err != nil {
		t.Fatalf("rate: %v", err)
	}
	ctx, err := raterContext(db, 81)
	if err != nil {
		t.Fatalf("raterContext: %v", err)
	}
	if ctx.Complexity != "" || ctx.Risk != "high" {
		t.Errorf("ratings = %q/%q, want unrated/high", ctx.Complexity, ctx.Risk)
	}
}
