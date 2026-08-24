package monitor

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

// insertResumeSession inserts a session with an explicit UUID, task and
// last_activity so ResolveResumeTarget's ordering and filters can be
// exercised. A nil uuid/taskID stores SQL NULL. Returns the new sessions.id.
func insertResumeSession(t *testing.T, db *sql.DB, projectID int64, taskID *int64, uuid *string, lastActivity string) int64 {
	t.Helper()
	var taskArg, uuidArg any
	if taskID != nil {
		taskArg = *taskID
	}
	if uuid != nil {
		uuidArg = *uuid
	}
	res, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, task_id, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'ended', ?, ?, ?)`,
		uuidArg, projectID, taskArg, lastActivity, lastActivity,
	)
	if err != nil {
		t.Fatalf("insert resume session: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func ptrInt64(v int64) *int64 { return &v }
func ptrStr(v string) *string { return &v }

func TestResolveResumeTarget_TaskFirstPicksMostRecent(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	seedTask(t, db, 10, 1, "t10", "underway")

	// Two sessions on task 10; the later last_activity must win.
	insertResumeSession(t, db, 1, ptrInt64(10), ptrStr("old-uuid-1111"), "2026-06-20T00:00:00")
	newID := insertResumeSession(t, db, 1, ptrInt64(10), ptrStr("new-uuid-2222"), "2026-06-25T00:00:00")

	for _, ref := range []string{"E-10", "e-10", "10"} {
		got, err := ResolveResumeTarget(ref)
		if err != nil {
			t.Fatalf("ResolveResumeTarget(%q): %v", ref, err)
		}
		if got.EndlessID != newID || got.SessionID != "new-uuid-2222" {
			t.Errorf("ref %q → id %d/%q, want %d/new-uuid-2222", ref, got.EndlessID, got.SessionID, newID)
		}
		if got.TaskID == nil || *got.TaskID != 10 {
			t.Errorf("ref %q → task_id %v, want 10", ref, got.TaskID)
		}
	}
}

func TestResolveResumeTarget_TaskWithoutUUIDNotResumable(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	seedTask(t, db, 10, 1, "t10", "underway")

	// A dispatched-but-never-started bg agent: task_id set, UUID NULL.
	insertResumeSession(t, db, 1, ptrInt64(10), nil, "2026-06-20T00:00:00")

	_, err := ResolveResumeTarget("E-10")
	if err == nil || !strings.Contains(err.Error(), "no resumable Claude session") {
		t.Fatalf("want no-resumable-session error, got %v", err)
	}
}

func TestResolveResumeTarget_BareIntFallsBackToSessionID(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	// A session with no task; its integer id is the only way to reach it.
	sid := insertResumeSession(t, db, 1, nil, ptrStr("loose-uuid-3333"), "2026-06-20T00:00:00")

	got, err := ResolveResumeTarget(strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatalf("ResolveResumeTarget(session id): %v", err)
	}
	if got.EndlessID != sid || got.SessionID != "loose-uuid-3333" {
		t.Errorf("got %d/%q, want %d/loose-uuid-3333", got.EndlessID, got.SessionID, sid)
	}
	if got.TaskID != nil {
		t.Errorf("task_id = %v, want nil", got.TaskID)
	}
}

// E-1918: `ES-<n>` names the session id space explicitly, so it must resolve to
// sessions.id <n> and never fall back to task E-<n>'s session — the collision the
// prefix exists to prevent. The fixture makes both readings available: task 10
// has its own session, and 10 is also a valid sessions.id.
func TestResolveResumeTarget_ESPrefixIsSessionExplicit(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	seedTask(t, db, 10, 1, "t10", "underway")

	taskSess := insertResumeSession(t, db, 1, ptrInt64(10), ptrStr("task-uuid-1111"), "2026-06-25T00:00:00")
	// Force a session whose *id* is 10, colliding with the task id.
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, started_at, last_activity)
		 VALUES (10, 'loose-uuid-2222', 1, 'claude', 'ended', '2026-06-20T00:00:00', '2026-06-20T00:00:00')`,
	); err != nil {
		t.Fatalf("insert colliding session: %v", err)
	}

	for _, ref := range []string{"ES-10", "es-10", "Es-10"} {
		got, err := ResolveResumeTarget(ref)
		if err != nil {
			t.Fatalf("ResolveResumeTarget(%q): %v", ref, err)
		}
		if got.EndlessID != 10 || got.SessionID != "loose-uuid-2222" {
			t.Errorf("ref %q → id %d/%q, want 10/loose-uuid-2222", ref, got.EndlessID, got.SessionID)
		}
	}

	// The other two forms are unchanged: task-explicit and task-first.
	for _, ref := range []string{"E-10", "10"} {
		got, err := ResolveResumeTarget(ref)
		if err != nil {
			t.Fatalf("ResolveResumeTarget(%q): %v", ref, err)
		}
		if got.EndlessID != taskSess {
			t.Errorf("ref %q → id %d, want task session %d", ref, got.EndlessID, taskSess)
		}
	}
}

// A miss on the ES- branch stays a miss: falling through to the task lookup is
// exactly the silent re-interpretation the prefix rules out.
func TestResolveResumeTarget_ESPrefixNeverFallsBackToTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	seedTask(t, db, 10, 1, "t10", "underway")
	insertResumeSession(t, db, 1, ptrInt64(10), ptrStr("task-uuid-1111"), "2026-06-25T00:00:00")

	_, err := ResolveResumeTarget("ES-10")
	if err == nil || !strings.Contains(err.Error(), "no session with id 10") {
		t.Fatalf("want no-session-with-id error, got %v", err)
	}

	_, err = ResolveResumeTarget("ES-nope")
	if err == nil || !strings.Contains(err.Error(), "integer session id") {
		t.Fatalf("want non-integer ES- diagnostic, got %v", err)
	}
}

// The project fields back the task-less auto-create path, so they must be filled
// on the branch that has no task — not only alongside a worktree.
func TestResolveResumeTarget_CarriesProjectOnTaskLessSession(t *testing.T) {
	db := withTestDB(t)
	root := tempProjectRoot(t)
	seedProject(t, db, 7, "p7", root)
	sid := insertResumeSession(t, db, 7, nil, ptrStr("loose-uuid-3333"), "2026-06-20T00:00:00")

	got, err := ResolveResumeTarget(strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if got.TaskID != nil {
		t.Fatalf("task_id = %v, want nil (the task-less branch)", got.TaskID)
	}
	if got.ProjectID != 7 {
		t.Errorf("project_id = %d, want 7", got.ProjectID)
	}
	if got.ProjectPath != root {
		t.Errorf("project_path = %q, want %q", got.ProjectPath, root)
	}
}

// A session whose project row is gone must still resolve — the Python caller
// owns the "nowhere to create a task" diagnostic, and every other caller of this
// target is indifferent to the project.
func TestResolveResumeTarget_MissingProjectLeavesPathEmpty(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	sid := insertResumeSession(t, db, 1, nil, ptrStr("loose-uuid-4444"), "2026-06-20T00:00:00")
	if _, err := db.Exec("UPDATE sessions SET project_id = NULL WHERE id = ?", sid); err != nil {
		t.Fatalf("clear project_id: %v", err)
	}

	got, err := ResolveResumeTarget(strconv.FormatInt(sid, 10))
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if got.ProjectID != 0 || got.ProjectPath != "" {
		t.Errorf("project = %d/%q, want 0/\"\"", got.ProjectID, got.ProjectPath)
	}
}

func TestResolveResumeTarget_AmbiguousUUIDPrefix(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	insertResumeSession(t, db, 1, nil, ptrStr("abcd-1111"), "2026-06-20T00:00:00")
	insertResumeSession(t, db, 1, nil, ptrStr("abce-2222"), "2026-06-21T00:00:00")

	_, err := ResolveResumeTarget("ab")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("want ambiguous-prefix error, got %v", err)
	}

	// A prefix unique to one row still resolves.
	got, err := ResolveResumeTarget("abcd")
	if err != nil {
		t.Fatalf("unique prefix: %v", err)
	}
	if got.SessionID != "abcd-1111" {
		t.Errorf("got %q, want abcd-1111", got.SessionID)
	}
}
