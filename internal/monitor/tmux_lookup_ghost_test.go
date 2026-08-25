package monitor

import (
	"database/sql"
	"errors"
	"testing"
)

// Pane-id reuse across tmux server restarts — the E-1530 family, re-pinned
// against E-1898's mechanism.
//
// E-1530 fixed reuse DEFENSIVELY: it filtered `state != 'ended'` in every
// reader, NULLed `process` whenever a row ended (in code AND in two schema
// triggers), and ended any row colliding on a pane string. All of that existed
// because "%414" alone cannot distinguish the pane it names today from the pane
// it named before the last tmux restart.
//
// E-1898 made the distinction structural: identity is (server_uuid, address),
// so a reissued pane is a DIFFERENT processes row and cannot collide at all.
// The defensive machinery is gone with it — the two `sessions_null_process_on_end_*`
// triggers and their tests, and the collision-invalidation write.
//
// The GUARANTEES are unchanged and still tested here; only their mechanism
// moved. What is new is that a binding now SURVIVES the session ending, which
// the old tests asserted the opposite of.

// TestGetTaskForPane_SkipsEndedRows keeps the original E-1530 guarantee
// for the case identity does not cover: the same session identity ending and a
// new one starting in the same pane on the SAME server. Both rows share a
// process_id, so only `state != 'ended'` separates them.
func TestGetTaskForPane_SkipsEndedRows(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	seedGhostTask(t, db, 111, "ghost task")
	seedGhostTask(t, db, 222, "live task")

	pid := mustSeedPane(t, db, TestServerUUID, fakePane)

	// The ghost is NEWER than the live row, to prove the state filter wins
	// rather than the ORDER BY.
	seedPaneSession(t, db, "sess-ghost", pid, "ended", 111, "2026-05-21T00:00:00")
	seedPaneSession(t, db, "sess-live", pid, "working", 222, "2026-05-20T00:00:00")

	info, err := GetTaskForPane(fakePane)
	if err != nil {
		t.Fatalf("GetTaskForPane: %v", err)
	}
	if info.TaskID != 222 {
		t.Errorf("TaskID = %d, want 222 (live), got the ghost row", info.TaskID)
	}
}

// TestGetTaskForPane_GhostOnlyReturnsNoTask is the standalone version:
// with ONLY an ended row on the pane, the lookup surfaces ErrNoTask
// rather than the stale task.
func TestGetTaskForPane_GhostOnlyReturnsNoTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	seedGhostTask(t, db, 333, "ghost only")

	pid := mustSeedPane(t, db, TestServerUUID, fakePane)
	seedPaneSession(t, db, "sess-ghost-only", pid, "ended", 333, "2026-05-21T00:00:00")

	_, err := GetTaskForPane(fakePane)
	if !errors.Is(err, ErrNoTask) {
		t.Errorf("ghost-only: got %v, want ErrNoTask", err)
	}
}

// TestGetTaskForPane_ReusedPaneOnNewServerIsDifferentIdentity is the
// STRUCTURAL replacement for everything E-1530 had to do defensively.
//
// Same pane string, two tmux servers. The prior server's session is left fully
// intact — `working`, with its binding — which under the old string-keyed
// lookup is the worst case: a live-looking row whose pane id matches. It must
// still be invisible from the current server, purely because its processes row
// is a different identity.
func TestGetTaskForPane_ReusedPaneOnNewServerIsDifferentIdentity(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	seedGhostTask(t, db, 444, "previous server's task")
	seedGhostTask(t, db, 555, "current server's task")

	// Same address, different servers -> two distinct identities.
	oldPID := mustSeedPane(t, db, "server-before-restart", fakePane)
	newPID := mustSeedPane(t, db, TestServerUUID, fakePane)
	if oldPID == newPID {
		t.Fatalf("pane %q on two servers collapsed to one processes row (id=%d)", fakePane, oldPID)
	}

	// Deliberately NOT ended, and more recently active than the live row.
	seedPaneSession(t, db, "sess-old-server", oldPID, "working", 444, "2026-05-21T00:00:00")
	seedPaneSession(t, db, "sess-this-server", newPID, "working", 555, "2026-05-20T00:00:00")

	info, err := GetTaskForPane(fakePane)
	if err != nil {
		t.Fatalf("GetTaskForPane: %v", err)
	}
	if info.TaskID != 555 {
		t.Errorf("TaskID = %d, want 555; the previous server's binding won the lookup", info.TaskID)
	}
}

// TestEndSession_PreservesBinding INVERTS the old TestEndSession_NullsProcess.
//
// Ending a session used to clear its pane binding, because an ended row holding
// "%414" could shadow a reissued "%414". It cannot any more, so the erasure has
// no purpose — and it destroyed evidence. "Session X ran on pane %414 of server
// Y" stays true after the session ends, and on 2026-08-05 that history was
// exactly what diagnosed the incident. Ending records a fact; it must not
// delete one.
func TestEndSession_PreservesBinding(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")

	pid := mustSeedPane(t, db, TestServerUUID, fakePane)
	seedPaneSession(t, db, "sess-end", pid, "working", 0, "2026-05-20T00:00:00")

	if err := EndSession("sess-end"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	var gotPID *int64
	var state string
	if err := db.QueryRow(
		"SELECT process_id, state FROM sessions WHERE session_id = ?", "sess-end",
	).Scan(&gotPID, &state); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "ended" {
		t.Errorf("state = %q, want ended", state)
	}
	if gotPID == nil {
		t.Fatal("process_id was cleared on end; the binding is history and must survive")
	}
	if *gotPID != pid {
		t.Errorf("process_id = %d, want %d (unchanged)", *gotPID, pid)
	}
}

// TestTouchSession_SameServerCollisionLeavesPriorRowAlone replaces
// TestTouchSession_NullsCollidedRowProcess, and pins E-1468's complaint as a
// guarantee rather than a known bug.
//
// Two session identities legitimately share one pane on one server (E-1468's
// repro: a land run from the main checkout alongside the worktree's session).
// The old code ended the prior row on the theory that a pane hosts one harness
// — killing a live session. Nothing is written now: both rows stand and readers
// order by last_activity.
func TestTouchSession_SameServerCollisionLeavesPriorRowAlone(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")

	pid := mustSeedPane(t, db, TestServerUUID, fakePane)
	seedPaneSession(t, db, "sess-collide-old", pid, "working", 0, "2026-05-20T00:00:00")

	if err := TouchSession("sess-collide-new", "claude", fakePane, 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	var gotPID *int64
	var state string
	if err := db.QueryRow(
		"SELECT process_id, state FROM sessions WHERE session_id = ?", "sess-collide-old",
	).Scan(&gotPID, &state); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "working" {
		t.Errorf("prior occupant state = %q, want working — nothing observed it die", state)
	}
	if gotPID == nil || *gotPID != pid {
		t.Errorf("prior occupant process_id = %v, want %d (untouched)", gotPID, pid)
	}

	// ...and the new session bound to the SAME identity, since it is the same
	// pane on the same server.
	var newPID *int64
	if err := db.QueryRow(
		"SELECT process_id FROM sessions WHERE session_id = ?", "sess-collide-new",
	).Scan(&newPID); err != nil {
		t.Fatalf("read new: %v", err)
	}
	if newPID == nil || *newPID != pid {
		t.Errorf("new session process_id = %v, want %d", newPID, pid)
	}
}

// ── fixtures ────────────────────────────────────────────────────────────────

// mustSeedPane creates (or reuses) the processes row for a pane on a server and
// returns its id. Taking serverUUID explicitly is what lets a test seed the same
// pane on two different servers, which is the whole point of several tests here.
func mustSeedPane(t *testing.T, db *sql.DB, serverUUID, pane string) int64 {
	t.Helper()
	id, err := SeedPaneProcess(db, serverUUID, pane)
	if err != nil {
		t.Fatalf("seed pane %q on %q: %v", pane, serverUUID, err)
	}
	return id
}

// seedPaneSession inserts a sessions row bound to processID. taskID 0 means
// "holds no task" (stored NULL).
func seedPaneSession(t *testing.T, db *sql.DB, sessionID string, processID int64, state string, taskID int64, lastActivity string) {
	t.Helper()
	var taskVal any
	if taskID != 0 {
		taskVal = taskID
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, task_id, last_activity)
		 VALUES (?, 1, 'claude', ?, ?, ?, ?)`,
		sessionID, state, processID, taskVal, lastActivity,
	); err != nil {
		t.Fatalf("seed session %q: %v", sessionID, err)
	}
}

// seedGhostTask inserts a minimal underway task for these fixtures.
func seedGhostTask(t *testing.T, db *sql.DB, id int64, title string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, type_id, phase) VALUES (?, 1, ?, 'underway', 1, 'now')",
		id, title,
	); err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}
