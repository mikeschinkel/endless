package monitor

import (
	"database/sql"
	"testing"
)

// snTask inserts a task with explicit phase/status/text/type so the
// session-next query's canonicalization and has_text columns can be exercised.
func snTask(t *testing.T, db *sql.DB, id, projectID int64, status, phase, text string) {
	t.Helper()
	var textVal any
	if text != "" {
		textVal = text
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, phase, text)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, projectID, "task-"+status, status, phase, textVal,
	); err != nil {
		t.Fatalf("snTask id=%d: %v", id, err)
	}
}

// snSession inserts a session with an explicit id and active_task_id so the
// row-set membership (sessions on the focal task) and in_flight decoration can
// be driven directly.
func snSession(t *testing.T, db *sql.DB, id, projectID, activeTask int64, state string) {
	t.Helper()
	var at any
	if activeTask != 0 {
		at = activeTask
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
		 VALUES (?, NULL, ?, 'claude', ?, ?, 1, '2026-06-20T00:00:00', '2026-06-20T00:00:00')`,
		id, projectID, state, at,
	); err != nil {
		t.Fatalf("snSession id=%d: %v", id, err)
	}
}

func snSessionTask(t *testing.T, db *sql.DB, sessionID, taskID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (?, ?, '2026-06-20T00:00:00', '2026-06-20T00:00:00')`,
		sessionID, taskID,
	); err != nil {
		t.Fatalf("snSessionTask s=%d t=%d: %v", sessionID, taskID, err)
	}
}

func snBlocks(t *testing.T, db *sql.DB, blockerID, blockedID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', ?, 'task', ?, 'blocks')`,
		blockerID, blockedID,
	); err != nil {
		t.Fatalf("snBlocks %d->%d: %v", blockerID, blockedID, err)
	}
}

func snRowByID(rows []SessionNextRow, id int64) (SessionNextRow, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return SessionNextRow{}, false
}

// TestSessionNextRows_RowSetAndDecorations drives the whole query: the row set
// (sessions on focal ∪ focal ∪ parent's task), the focal/parent/in_flight
// decorations, the block counts, and the terminal-status filter.
func TestSessionNextRows_RowSetAndDecorations(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, sibling, parentTask, blocker, doneSibling = 100, 101, 200, 300, 400

	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, sibling, 1, "ready", "next", "has-plan")
	snTask(t, db, parentTask, 1, "ready", "later", "")
	snTask(t, db, blocker, 1, "underway", "now", "") // open blocker of focal
	snTask(t, db, doneSibling, 1, "confirmed", "now", "")

	// s1 is on the focal task and has touched focal, sibling, and the done one.
	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, sibling)
	snSessionTask(t, db, 1, doneSibling)
	// s2 is the parent (spawning) session; its active task is parentTask.
	snSession(t, db, 2, 1, parentTask, "working")
	// s3 is a live session on the sibling → sibling is in_flight.
	snSession(t, db, 3, 1, sibling, "working")

	snBlocks(t, db, blocker, focal) // focal is blocked by an open task
	snBlocks(t, db, focal, sibling) // focal blocks the sibling

	rows, err := SessionNextRows(focal, 2, false)
	if err != nil {
		t.Fatalf("SessionNextRows: %v", err)
	}

	// doneSibling is terminal and neither focal nor parent → filtered out.
	if _, ok := snRowByID(rows, doneSibling); ok {
		t.Errorf("terminal task %d should be excluded without --all", doneSibling)
	}
	for _, id := range []int64{focal, sibling, parentTask} {
		if _, ok := snRowByID(rows, id); !ok {
			t.Errorf("expected task %d in row set, missing", id)
		}
	}

	f, _ := snRowByID(rows, focal)
	if !f.IsFocal || f.IsParent || f.InFlight {
		t.Errorf("focal decorations wrong: %+v", f)
	}
	if f.BlockedByN != 1 {
		t.Errorf("focal BlockedByN = %d, want 1", f.BlockedByN)
	}
	if f.BlocksN != 1 {
		t.Errorf("focal BlocksN = %d, want 1", f.BlocksN)
	}

	p, _ := snRowByID(rows, parentTask)
	if !p.IsParent || p.IsFocal {
		t.Errorf("parent decorations wrong: %+v", p)
	}

	s, _ := snRowByID(rows, sibling)
	if !s.InFlight {
		t.Errorf("sibling should be in_flight (live session on it): %+v", s)
	}
	if !s.HasText {
		t.Errorf("sibling has plan text, HasText should be true: %+v", s)
	}
}

// TestSessionNextRows_AllIncludesDoneWork confirms --all surfaces terminal rows
// that are part of the row set.
func TestSessionNextRows_AllIncludesDoneWork(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, done = 10, 20
	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, done, 1, "confirmed", "now", "")
	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, done)

	rows, err := SessionNextRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionNextRows: %v", err)
	}
	if _, ok := snRowByID(rows, done); !ok {
		t.Errorf("--all should include terminal task %d", done)
	}
}

// TestSessionNextRows_FocalDependents drives the E-1685 read-time dependents
// UNION: the focal task's direct dependents (tasks it blocks) appear as rows
// purely from task_deps, with no session_tasks membership; they carry ⊗
// (BlockedByN>0) while the focal is open and shed it once the focal lands; and a
// terminal dependent is omitted by default but surfaced under --all.
func TestSessionNextRows_FocalDependents(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, dep, doneDep = 500, 501, 502
	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, dep, 1, "ready", "next", "")
	snTask(t, db, doneDep, 1, "confirmed", "next", "")

	// The only session is on the focal and has touched ONLY the focal — neither
	// dependent is in session_tasks, so their presence proves the read-time UNION.
	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)

	snBlocks(t, db, focal, dep)     // focal blocks an open dependent
	snBlocks(t, db, focal, doneDep) // focal blocks a terminal dependent

	// ── focal open: dependent shown, carrying ⊗ (BlockedByN counts the open focal)
	rows, err := SessionNextRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionNextRows: %v", err)
	}
	d, ok := snRowByID(rows, dep)
	if !ok {
		t.Fatalf("open dependent %d should be a row while focal is open", dep)
	}
	if d.IsFocal || d.IsParent {
		t.Errorf("dependent should not be focal/parent: %+v", d)
	}
	if d.BlockedByN != 1 {
		t.Errorf("dependent BlockedByN = %d, want 1 (blocked by open focal)", d.BlockedByN)
	}
	if _, ok := snRowByID(rows, doneDep); ok {
		t.Errorf("terminal dependent %d should be omitted without --all", doneDep)
	}

	// ── --all surfaces the terminal dependent
	rowsAll, err := SessionNextRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionNextRows --all: %v", err)
	}
	if _, ok := snRowByID(rowsAll, doneDep); !ok {
		t.Errorf("--all should include terminal dependent %d", doneDep)
	}

	// ── focal lands (terminal): the dependent's ⊗ clears, it still shows
	if _, err := db.Exec(`UPDATE tasks SET status = 'confirmed' WHERE id = ?`, focal); err != nil {
		t.Fatalf("land focal: %v", err)
	}
	rows2, err := SessionNextRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionNextRows after land: %v", err)
	}
	d2, ok := snRowByID(rows2, dep)
	if !ok {
		t.Fatalf("dependent %d should still show after focal lands", dep)
	}
	if d2.BlockedByN != 0 {
		t.Errorf("dependent BlockedByN = %d after focal lands, want 0 (⊗ cleared)", d2.BlockedByN)
	}
}

// TestSessionNextRows_ZeroFocal returns nothing when no focal task resolves.
func TestSessionNextRows_ZeroFocal(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	rows, err := SessionNextRows(0, 0, false)
	if err != nil {
		t.Fatalf("SessionNextRows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("focal=0 should yield no rows, got %d", len(rows))
	}
}
