// Black-box tests for the internal/web query layer. These exercise the
// SQL contracts in queries.go via the SetTestDB seam (E-1506): each test
// stands up a fresh schema-applied SQLite DB, rebinds monitor.DB() to it
// for the lifetime of t, seeds the rows the query reads, and asserts on
// the returned shapes.
//
// Concurrency: SetTestDB mutates package-level state in monitor; no
// t.Parallel() in this file.
package web_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/web"
	"github.com/mikeschinkel/endless/internal/web/data"
)

// withMonitorDB stands up a fresh schema-applied SQLite DB and rebinds
// monitor.DB() to it for the lifetime of t via monitor.SetTestDB. The
// returned *sql.DB is the same handle the queries package will see when
// it calls monitor.DB() — tests use it directly to seed rows.
func withMonitorDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "endless.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)
	return db
}

// seedProject inserts a row into projects. NOT NULL columns are name and
// path; status defaults to 'active'. Pass an explicit status when the
// query under test filters on it.
func seedProject(t *testing.T, db *sql.DB, id int64, name, path, status string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path, status) VALUES (?, ?, ?, ?)",
		id, name, path, status,
	); err != nil {
		t.Fatalf("seed project id=%d: %v", id, err)
	}
}

// seedTask inserts a tasks row with explicit id/project/title/status. The
// tasks table requires title (NOT NULL) and project_id; everything else
// has a default. description is set non-NULL because several queries
// scan it directly into a Go string without COALESCE.
func seedTask(t *testing.T, db *sql.DB, id, projectID int64, title, status string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, description, status) VALUES (?, ?, ?, ?, ?)",
		id, projectID, title, title+" desc", status,
	); err != nil {
		t.Fatalf("seed task id=%d: %v", id, err)
	}
}

// ---------------------------------------------------------------------
// GetDashboardProjects
// ---------------------------------------------------------------------

// TestGetDashboardProjects_FiltersByStatus pins the WHERE clause: only
// projects with status in (active, paused, idea) appear; archived,
// abandoned, etc. are excluded.
func TestGetDashboardProjects_FiltersByStatus(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "proj-active", "/tmp/proj-active", "active")
	seedProject(t, db, 2, "proj-paused", "/tmp/proj-paused", "paused")
	seedProject(t, db, 3, "proj-idea", "/tmp/proj-idea", "idea")
	seedProject(t, db, 4, "proj-archived", "/tmp/proj-archived", "archived")

	got := web.GetDashboardProjects()
	if len(got) != 3 {
		t.Fatalf("GetDashboardProjects len = %d, want 3", len(got))
	}
	for _, p := range got {
		if p.Status == "archived" {
			t.Errorf("archived project %q must not appear", p.Name)
		}
	}
}

// TestGetDashboardProjects_TaskCounts pins the four COALESCE/scalar
// subselects: TaskTotal, TaskCompleted, TaskInProgress, ActiveTasks each
// slice the same project's tasks differently. After E-1507 the queries
// no longer filter out type='decision' — decisions live in their own
// table, so the prior filter is unnecessary; this test verifies decisions
// in the dedicated table don't leak into the task counts.
func TestGetDashboardProjects_TaskCounts(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	seedTask(t, db, 10, 1, "in-prog", "in_progress")
	seedTask(t, db, 11, 1, "done", "completed")
	seedTask(t, db, 12, 1, "ready", "ready")
	seedTask(t, db, 13, 1, "plan", "needs_plan")
	// A row in the decisions table (E-1378) is in a different table and
	// must not contribute to any of the four task counts.
	if _, err := db.Exec(
		"INSERT INTO decisions (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		1, 1, "decision-x", "accepted",
	); err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	got := web.GetDashboardProjects()
	if len(got) != 1 {
		t.Fatalf("GetDashboardProjects len = %d, want 1", len(got))
	}
	p := got[0]
	if p.TaskTotal != 4 {
		t.Errorf("TaskTotal = %d, want 4 (decisions live in a separate table)", p.TaskTotal)
	}
	if p.TaskCompleted != 1 {
		t.Errorf("TaskCompleted = %d, want 1", p.TaskCompleted)
	}
	if p.TaskInProgress != 1 {
		t.Errorf("TaskInProgress = %d, want 1", p.TaskInProgress)
	}
	// ActiveTasks counts needs_plan + ready + in_progress.
	if p.ActiveTasks != 3 {
		t.Errorf("ActiveTasks = %d, want 3", p.ActiveTasks)
	}
}

// TestGetDashboardProjects_OrderByLastActivity pins the ORDER BY:
// projects with more-recent activity sort first; ties fall through to
// name ASC.
func TestGetDashboardProjects_OrderByLastActivity(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "alpha", "/tmp/alpha", "active")
	seedProject(t, db, 2, "bravo", "/tmp/bravo", "active")
	// bravo has the more-recent activity; alpha is older.
	if _, err := db.Exec(
		"INSERT INTO activity (project_id, source, created_at) VALUES (?, ?, ?)",
		1, "hook", "2026-01-01T00:00:00",
	); err != nil {
		t.Fatalf("seed activity alpha: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO activity (project_id, source, created_at) VALUES (?, ?, ?)",
		2, "hook", "2026-05-01T00:00:00",
	); err != nil {
		t.Fatalf("seed activity bravo: %v", err)
	}

	got := web.GetDashboardProjects()
	if len(got) != 2 {
		t.Fatalf("GetDashboardProjects len = %d, want 2", len(got))
	}
	if got[0].Name != "bravo" {
		t.Errorf("first = %q, want %q (most recent activity)", got[0].Name, "bravo")
	}
	if got[1].Name != "alpha" {
		t.Errorf("second = %q, want %q", got[1].Name, "alpha")
	}
}

// ---------------------------------------------------------------------
// GetCurrentWork
// ---------------------------------------------------------------------

// TestGetCurrentWork_OnlyInProgress pins WHERE pi.status = 'in_progress':
// ready/needs_plan/completed tasks must not appear.
func TestGetCurrentWork_OnlyInProgress(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	seedTask(t, db, 10, 1, "active work", "in_progress")
	seedTask(t, db, 11, 1, "queued", "ready")
	seedTask(t, db, 12, 1, "done", "completed")

	got := web.GetCurrentWork()
	if len(got) != 1 {
		t.Fatalf("GetCurrentWork len = %d, want 1", len(got))
	}
	if got[0].TaskID != 10 {
		t.Errorf("TaskID = %d, want 10", got[0].TaskID)
	}
	if got[0].Project != "p" {
		t.Errorf("Project = %q, want %q", got[0].Project, "p")
	}
}

// TestGetCurrentWork_EmptyWhenNoneInProgress pins the empty-result path:
// the function returns nil/empty when no rows match.
func TestGetCurrentWork_EmptyWhenNoneInProgress(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	seedTask(t, db, 10, 1, "ready", "ready")

	got := web.GetCurrentWork()
	if len(got) != 0 {
		t.Errorf("GetCurrentWork len = %d, want 0", len(got))
	}
}

// ---------------------------------------------------------------------
// GetRecentActivity
// ---------------------------------------------------------------------

// TestGetRecentActivity_RespectsLimit pins the LIMIT ? binding: with
// three activity rows and limit=2, exactly two are returned.
func TestGetRecentActivity_RespectsLimit(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	for i, ts := range []string{
		"2026-01-01T00:00:00",
		"2026-02-01T00:00:00",
		"2026-03-01T00:00:00",
	} {
		if _, err := db.Exec(
			"INSERT INTO activity (id, project_id, source, created_at) VALUES (?, ?, ?, ?)",
			i+1, 1, "hook", ts,
		); err != nil {
			t.Fatalf("seed activity %d: %v", i, err)
		}
	}

	got := web.GetRecentActivity(2)
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

// TestGetRecentActivity_OrderByCreatedAtDesc pins ORDER BY a.created_at
// DESC: the newest row appears first.
func TestGetRecentActivity_OrderByCreatedAtDesc(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	if _, err := db.Exec(
		"INSERT INTO activity (id, project_id, source, created_at) VALUES (?, ?, ?, ?)",
		1, 1, "hook-old", "2026-01-01T00:00:00",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO activity (id, project_id, source, created_at) VALUES (?, ?, ?, ?)",
		2, 1, "hook-new", "2026-05-01T00:00:00",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := web.GetRecentActivity(10)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Source != "hook-new" {
		t.Errorf("first source = %q, want %q", got[0].Source, "hook-new")
	}
}

// TestGetRecentActivity_ExtractsEventFromContext pins the
// json_extract(session_context,'$.event') projection: the event field is
// pulled out of the JSON blob, missing keys COALESCE to "".
func TestGetRecentActivity_ExtractsEventFromContext(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	if _, err := db.Exec(
		`INSERT INTO activity (id, project_id, source, session_context, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		1, 1, "hook", `{"event":"SessionStart"}`, "2026-05-01T00:00:00",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := web.GetRecentActivity(1)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Event != "SessionStart" {
		t.Errorf("Event = %q, want %q", got[0].Event, "SessionStart")
	}
}

// ---------------------------------------------------------------------
// GetProjectDetail
// ---------------------------------------------------------------------

// TestGetProjectDetail_FoundByName pins the happy path: a project
// registered with the given name returns a populated row with name/path
// matching the seed.
func TestGetProjectDetail_FoundByName(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme", "active")

	p, err := web.GetProjectDetail("acme")
	if err != nil {
		t.Fatalf("GetProjectDetail: %v", err)
	}
	if p == nil {
		t.Fatalf("GetProjectDetail returned nil")
	}
	if p.ID != 1 || p.Name != "acme" || p.Path != "/tmp/acme" {
		t.Errorf("got %+v, want id=1 name=acme path=/tmp/acme", p)
	}
}

// TestGetProjectDetail_MissingReturnsError pins the not-found path:
// a name with no matching row returns a sql.ErrNoRows-flavoured error
// rather than a stray empty struct.
func TestGetProjectDetail_MissingReturnsError(t *testing.T) {
	withMonitorDB(t)

	p, err := web.GetProjectDetail("does-not-exist")
	if err == nil {
		t.Errorf("expected error for missing project, got nil; p=%+v", p)
	}
}

// ---------------------------------------------------------------------
// GetProjectTasks
// ---------------------------------------------------------------------

// TestGetProjectTasks_FiltersByProject pins the WHERE clause: only rows
// with project_id=? appear. After E-1507 the queries no longer filter
// type='decision' (decisions live in their own table — E-1378), so this
// test also verifies that a row in the decisions table doesn't leak
// into the task list.
func TestGetProjectTasks_FiltersByProject(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p1", "/tmp/p1", "active")
	seedProject(t, db, 2, "p2", "/tmp/p2", "active")
	seedTask(t, db, 10, 1, "p1-task", "ready")
	seedTask(t, db, 11, 2, "p2-task", "ready")
	if _, err := db.Exec(
		"INSERT INTO decisions (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		1, 1, "p1-decision", "accepted",
	); err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	got := web.GetProjectTasks(1)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (only p1's task; decisions live elsewhere)", len(got))
	}
	if got[0].ID != 10 {
		t.Errorf("ID = %d, want 10", got[0].ID)
	}
}

// TestGetProjectTasks_TreeOrderAndDepth pins the depth-first flattening:
// each root precedes its children, depth increments per level, and the
// SiblingNum index is 1-based per parent.
func TestGetProjectTasks_TreeOrderAndDepth(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	seedTask(t, db, 100, 1, "root-a", "ready")
	seedTask(t, db, 101, 1, "root-b", "ready")
	// Child of root-a.
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, parent_id, title, description, status) VALUES (?, ?, ?, ?, ?, ?)",
		200, 1, 100, "child-a1", "child-a1 desc", "ready",
	); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	got := web.GetProjectTasks(1)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// The child must follow its parent in the flattened list and have
	// depth=1. Root depths must be 0.
	byID := map[int64]int{}
	for i, tv := range got {
		byID[tv.ID] = i
	}
	if byID[100] > byID[200] {
		t.Errorf("child-a1 (id=200) must follow root-a (id=100); indices: %v", byID)
	}
	for _, tv := range got {
		switch tv.ID {
		case 100, 101:
			if tv.Depth != 0 {
				t.Errorf("root task id=%d depth = %d, want 0", tv.ID, tv.Depth)
			}
		case 200:
			if tv.Depth != 1 {
				t.Errorf("child task id=200 depth = %d, want 1", tv.Depth)
			}
		}
	}
}

// TestGetProjectTasks_ExcludeStatuses pins the variadic excludeStatuses:
// the values are interpolated into the child_count NOT IN clause; with
// none passed the child_count excludes only 'completed' by default. We
// verify the function still returns non-decision rows when explicit
// excludes are passed (the excludes shape child_count, not the main
// WHERE clause).
func TestGetProjectTasks_ExcludeStatuses(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	seedTask(t, db, 10, 1, "parent", "ready")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, parent_id, title, description, status) VALUES (?, ?, ?, ?, ?, ?)",
		11, 1, 10, "child", "child desc", "completed",
	); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	// Default: child_count NOT IN ('completed') — the completed child
	// is excluded, so parent's child_count is 0.
	def := web.GetProjectTasks(1)
	var parent *struct {
		ID         int64
		ChildCount int
	}
	for _, tv := range def {
		if tv.ID == 10 {
			parent = &struct {
				ID         int64
				ChildCount int
			}{tv.ID, tv.ChildCount}
		}
	}
	if parent == nil {
		t.Fatalf("parent id=10 not returned")
	}
	if parent.ChildCount != 0 {
		t.Errorf("default child_count = %d, want 0 (completed child excluded)", parent.ChildCount)
	}

	// With excludes that don't include 'completed', the completed child
	// IS counted (child_count NOT IN ('ready') matches the completed row).
	got := web.GetProjectTasks(1, "ready")
	for _, tv := range got {
		if tv.ID == 10 && tv.ChildCount != 1 {
			t.Errorf("excludeStatuses=ready child_count = %d, want 1", tv.ChildCount)
		}
	}
}

// ---------------------------------------------------------------------
// GetProjectTaskGroups
// ---------------------------------------------------------------------

// TestGetProjectTaskGroups_GroupsByParentTask pins the post-fix shape
// (E-1506): tasks with the same parent_id collect into one TaskGroup
// whose GroupID is the parent id and GroupName is the parent's title.
// Tasks with NULL parent_id collapse into the catch-all "Ungrouped"
// bucket (group_id = 0).
func TestGetProjectTaskGroups_GroupsByParentTask(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	// Parent task (the "plan").
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		100, 1, "Migrate auth", "needs_plan",
	); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	// Children with that parent.
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, parent_id, title, status, sort_order) VALUES (?, ?, ?, ?, ?, ?)",
		101, 1, 100, "step one", "in_progress", 1,
	); err != nil {
		t.Fatalf("seed child 1: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, parent_id, title, status, sort_order) VALUES (?, ?, ?, ?, ?, ?)",
		102, 1, 100, "step two", "ready", 2,
	); err != nil {
		t.Fatalf("seed child 2: %v", err)
	}
	// Orphan (no parent) — must land in the Ungrouped bucket.
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		103, 1, "loose end", "ready",
	); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	got := web.GetProjectTaskGroups(1)
	if len(got) < 2 {
		t.Fatalf("expected at least 2 groups (Migrate auth + Ungrouped), got %d: %+v", len(got), got)
	}
	groups := map[string]int{}
	for _, g := range got {
		groups[g.GroupName] = len(g.Tasks)
	}
	if groups["Migrate auth"] != 2 {
		t.Errorf("expected 2 tasks under 'Migrate auth', got %d (groups=%v)", groups["Migrate auth"], groups)
	}
	if groups["Ungrouped"] < 1 {
		t.Errorf("expected at least 1 task under 'Ungrouped', got %d", groups["Ungrouped"])
	}
}

// TestGetProjectTaskGroups_TotalAndDoneCountChildren pins the
// per-group counters: Total counts every child task whose parent_id
// matches the group, regardless of status; Done counts only those with
// status='completed'.
func TestGetProjectTaskGroups_TotalAndDoneCountChildren(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		200, 1, "Big plan", "in_progress",
	); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	for i, status := range []string{"in_progress", "completed", "completed", "ready"} {
		if _, err := db.Exec(
			"INSERT INTO tasks (id, project_id, parent_id, title, status) VALUES (?, ?, ?, ?, ?)",
			201+i, 1, 200, "child", status,
		); err != nil {
			t.Fatalf("seed child %d: %v", i, err)
		}
	}

	got := web.GetProjectTaskGroups(1)
	var bigPlan *data.TaskGroup
	for i := range got {
		if got[i].GroupName == "Big plan" {
			bigPlan = &got[i]
		}
	}
	if bigPlan == nil {
		t.Fatalf("Big plan group missing: %+v", got)
	}
	if bigPlan.Total != 4 {
		t.Errorf("Total = %d, want 4 (all children regardless of status)", bigPlan.Total)
	}
	if bigPlan.Done != 2 {
		t.Errorf("Done = %d, want 2 (status='completed' only)", bigPlan.Done)
	}
}

// ---------------------------------------------------------------------
// GetStatusDetail
// ---------------------------------------------------------------------

// TestGetStatusDetail_CombinesProjectAndTasks pins the composition:
// GetStatusDetail wraps GetProjectDetail + GetProjectTasks. The returned
// struct must carry the project payload AND the filtered task list.
func TestGetStatusDetail_CombinesProjectAndTasks(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme", "active")
	seedTask(t, db, 10, 1, "work", "ready")

	sd, err := web.GetStatusDetail("acme")
	if err != nil {
		t.Fatalf("GetStatusDetail: %v", err)
	}
	if sd == nil {
		t.Fatalf("GetStatusDetail returned nil")
	}
	if sd.Project.Name != "acme" {
		t.Errorf("Project.Name = %q, want %q", sd.Project.Name, "acme")
	}
	if len(sd.TaskItems) != 1 || sd.TaskItems[0].ID != 10 {
		t.Errorf("TaskItems = %+v, want one item with ID=10", sd.TaskItems)
	}
}

// TestGetStatusDetail_MissingProjectPropagatesError pins the error path
// of GetProjectDetail flowing through unchanged.
func TestGetStatusDetail_MissingProjectPropagatesError(t *testing.T) {
	withMonitorDB(t)

	sd, err := web.GetStatusDetail("nope")
	if err == nil {
		t.Errorf("expected error for missing project, got nil; sd=%+v", sd)
	}
}

// ---------------------------------------------------------------------
// GetProjectDependencies
// ---------------------------------------------------------------------

// TestGetProjectDependencies_TaskScoped pins the source_type='task'
// branch: a task_deps row whose source_id maps to a task in this
// project is returned with the task title resolved.
func TestGetProjectDependencies_TaskScoped(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p1", "/tmp/p1", "active")
	seedProject(t, db, 2, "p2", "/tmp/p2", "active")
	seedTask(t, db, 100, 1, "src-task", "ready")
	seedTask(t, db, 200, 2, "tgt-task", "ready")
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES (?, ?, ?, ?, ?)`,
		"task", 100, "task", 200, "blocks",
	); err != nil {
		t.Fatalf("seed dep: %v", err)
	}

	got := web.GetProjectDependencies(1)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	d := got[0]
	if d.SourceType != "task" || d.SourceID != 100 || d.SourceName != "src-task" {
		t.Errorf("source = (%s, %d, %q), want (task, 100, src-task)",
			d.SourceType, d.SourceID, d.SourceName)
	}
	if d.TargetID != 200 || d.TargetName != "tgt-task" {
		t.Errorf("target = (%d, %q), want (200, tgt-task)", d.TargetID, d.TargetName)
	}
	if d.DepType != "blocks" {
		t.Errorf("DepType = %q, want %q", d.DepType, "blocks")
	}
}

// TestGetProjectDependencies_ProjectScoped pins the
// source_type='project' branch: a task_deps row whose source_id is a
// project id matching ours is returned with the project name resolved.
func TestGetProjectDependencies_ProjectScoped(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p1", "/tmp/p1", "active")
	seedProject(t, db, 2, "p2", "/tmp/p2", "active")
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES (?, ?, ?, ?, ?)`,
		"project", 1, "project", 2, "depends_on",
	); err != nil {
		t.Fatalf("seed dep: %v", err)
	}

	got := web.GetProjectDependencies(1)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	d := got[0]
	if d.SourceType != "project" || d.SourceName != "p1" {
		t.Errorf("source = (%s, %q), want (project, p1)", d.SourceType, d.SourceName)
	}
	if d.TargetName != "p2" {
		t.Errorf("TargetName = %q, want %q", d.TargetName, "p2")
	}
}

// ---------------------------------------------------------------------
// GetProjectActivity
// ---------------------------------------------------------------------

// TestGetProjectActivity_ScopedAndLimited pins both filters: only rows
// for the named project_id appear, capped at limit.
func TestGetProjectActivity_ScopedAndLimited(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p1", "/tmp/p1", "active")
	seedProject(t, db, 2, "p2", "/tmp/p2", "active")
	if _, err := db.Exec(
		"INSERT INTO activity (project_id, source, created_at) VALUES (1, 'h1', '2026-01-01T00:00:00'), (1, 'h2', '2026-02-01T00:00:00'), (2, 'h-other', '2026-03-01T00:00:00')",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := web.GetProjectActivity(1, 10)
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (project=1 only)", len(got))
	}
	for _, a := range got {
		if a.Project != "p1" {
			t.Errorf("Project = %q, want %q", a.Project, "p1")
		}
	}

	// Limit clamps the result set.
	if lim := web.GetProjectActivity(1, 1); len(lim) != 1 {
		t.Errorf("limit=1 len = %d, want 1", len(lim))
	}
}

// ---------------------------------------------------------------------
// GetProjectNotes
// ---------------------------------------------------------------------

// TestGetProjectNotes_ScopedAndResolvedFlag pins the WHERE
// (project_id=?) and the int->bool conversion for the resolved column.
func TestGetProjectNotes_ScopedAndResolvedFlag(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p1", "/tmp/p1", "active")
	seedProject(t, db, 2, "p2", "/tmp/p2", "active")
	if _, err := db.Exec(
		`INSERT INTO notes (id, project_id, note_type, message, resolved, created_at)
		 VALUES (1, 1, 'staleness', 'open one', 0, '2026-02-01T00:00:00'),
		        (2, 1, 'sprawl',    'resolved one', 1, '2026-01-01T00:00:00'),
		        (3, 2, 'other',     'wrong project', 0, '2026-03-01T00:00:00')`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := web.GetProjectNotes(1)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (project=1)", len(got))
	}
	// Order is created_at DESC, so the open note (newer) is first, the
	// resolved one is second.
	if got[0].ID != 1 || got[0].Resolved {
		t.Errorf("got[0] = %+v, want id=1 resolved=false", got[0])
	}
	if got[1].ID != 2 || !got[1].Resolved {
		t.Errorf("got[1] = %+v, want id=2 resolved=true", got[1])
	}
}

// ---------------------------------------------------------------------
// UpdateTaskTitle / UpdateTaskStatus
// ---------------------------------------------------------------------

// TestUpdateTaskTitle_PersistsNewTitle pins that the UPDATE actually
// writes by reading the row back via the same DB handle the seam
// rebound.
func TestUpdateTaskTitle_PersistsNewTitle(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	seedTask(t, db, 10, 1, "old title", "ready")

	if err := web.UpdateTaskTitle(10, "new title"); err != nil {
		t.Fatalf("UpdateTaskTitle: %v", err)
	}
	var got string
	if err := db.QueryRow(
		"SELECT title FROM tasks WHERE id = ?", 10,
	).Scan(&got); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got != "new title" {
		t.Errorf("title = %q, want %q", got, "new title")
	}
}

// TestUpdateTaskStatus_PersistsNewStatus pins the second UPDATE wrapper:
// the row's status column is rewritten to the new value.
func TestUpdateTaskStatus_PersistsNewStatus(t *testing.T) {
	db := withMonitorDB(t)
	seedProject(t, db, 1, "p", "/tmp/p", "active")
	seedTask(t, db, 10, 1, "task", "ready")

	if err := web.UpdateTaskStatus(10, "in_progress"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	var got string
	if err := db.QueryRow(
		"SELECT status FROM tasks WHERE id = ?", 10,
	).Scan(&got); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got != "in_progress" {
		t.Errorf("status = %q, want %q", got, "in_progress")
	}
}
