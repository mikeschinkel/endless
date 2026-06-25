package monitor

import (
	"errors"
	"testing"
)

// TestGetActiveTaskForPane_SkipsEndedRows is the E-1530 regression: a
// ghost (state='ended') session whose process happens to match a pane
// id reused after a tmux server restart must not win the lookup over
// a live session for the same pane. Pre-fix, queryActiveTaskForPanes
// omitted the state filter and the most-recent ended row could shadow
// the live one (or, with no live row, mis-resolve the pane to a stale
// task).
func TestGetActiveTaskForPane_SkipsEndedRows(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, type_id, phase) VALUES (?, ?, ?, ?, ?, ?)",
		111, 1, "ghost task", "underway", 1, "now",
	); err != nil {
		t.Fatalf("seed ghost task: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, type_id, phase) VALUES (?, ?, ?, ?, ?, ?)",
		222, 1, "live task", "underway", 1, "now",
	); err != nil {
		t.Fatalf("seed live task: %v", err)
	}

	// Older ended session (the ghost) — note it predates the live one,
	// but a more recent ghost wins the bug just as easily.
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, active_task_id, last_activity)
		 VALUES (?, ?, 'claude', 'ended', ?, ?, '2026-05-21T00:00:00')`,
		"sess-ghost", 1, fakePane, 111,
	); err != nil {
		t.Fatalf("seed ghost session: %v", err)
	}
	// Live session for the same pane id (post-server-restart reuse) —
	// older last_activity to prove the filter, not ORDER BY, wins.
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, active_task_id, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, ?, '2026-05-20T00:00:00')`,
		"sess-live", 1, fakePane, 222,
	); err != nil {
		t.Fatalf("seed live session: %v", err)
	}

	info, err := GetActiveTaskForPane(fakePane)
	if err != nil {
		t.Fatalf("GetActiveTaskForPane: %v", err)
	}
	if info.TaskID != 222 {
		t.Errorf("TaskID = %d, want 222 (live), got the ghost row", info.TaskID)
	}
}

// TestGetActiveTaskForPane_GhostOnlyReturnsNoTask is the standalone
// version of the ghost-row guarantee: with ONLY an ended row matching
// the pane id, the lookup must surface ErrNoActiveTask, not the stale
// task. Pre-fix this returned the ghost's task.
func TestGetActiveTaskForPane_GhostOnlyReturnsNoTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, type_id, phase) VALUES (?, ?, ?, ?, ?, ?)",
		333, 1, "ghost only", "underway", 1, "now",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, active_task_id, last_activity)
		 VALUES (?, ?, 'claude', 'ended', ?, ?, '2026-05-21T00:00:00')`,
		"sess-ghost-only", 1, fakePane, 333,
	); err != nil {
		t.Fatalf("seed ghost: %v", err)
	}

	_, err := GetActiveTaskForPane(fakePane)
	if !errors.Is(err, ErrNoActiveTask) {
		t.Errorf("ghost-only: got %v, want ErrNoActiveTask", err)
	}
}

// TestEndSession_NullsProcess pins Layer A for EndSession: ending a
// session clears its `process` column. Without this, reused pane ids
// pull the dead row back into lookups after a tmux server restart.
func TestEndSession_NullsProcess(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-20T00:00:00')`,
		"sess-end", 1, fakePane,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if err := EndSession("sess-end"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	var process *string
	var state string
	if err := db.QueryRow(
		"SELECT process, state FROM sessions WHERE session_id = ?", "sess-end",
	).Scan(&process, &state); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "ended" {
		t.Errorf("state = %q, want ended", state)
	}
	if process != nil {
		t.Errorf("process = %q, want NULL", *process)
	}
}

// TestNullProcessOnEnd_TriggerUpdate pins Layer B (UPDATE): even when
// code forgets to NULL process at the call site, the trigger does it.
// Issued as a raw UPDATE that only flips state — the trigger fires on
// AFTER UPDATE OF state and NULLs process.
func TestNullProcessOnEnd_TriggerUpdate(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-20T00:00:00')`,
		"sess-trig-u", 1, fakePane,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := db.Exec(
		"UPDATE sessions SET state='ended' WHERE session_id = ?",
		"sess-trig-u",
	); err != nil {
		t.Fatalf("raw end UPDATE: %v", err)
	}

	var process *string
	if err := db.QueryRow(
		"SELECT process FROM sessions WHERE session_id = ?", "sess-trig-u",
	).Scan(&process); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if process != nil {
		t.Errorf("trigger did not NULL process; got %q", *process)
	}
}

// TestNullProcessOnEnd_TriggerInsert pins Layer B (INSERT): direct
// INSERTs that land a row already in state='ended' with a non-NULL
// process get NULL'd by the AFTER INSERT trigger.
func TestNullProcessOnEnd_TriggerInsert(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'ended', ?, '2026-05-20T00:00:00')`,
		"sess-trig-i", 1, fakePane,
	); err != nil {
		t.Fatalf("seed ended row: %v", err)
	}

	var process *string
	if err := db.QueryRow(
		"SELECT process FROM sessions WHERE session_id = ?", "sess-trig-i",
	).Scan(&process); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if process != nil {
		t.Errorf("INSERT trigger did not NULL process; got %q", *process)
	}
}

// TestTouchSession_NullsCollidedRowProcess pins Layer A for the
// collision-invalidation path in TouchSession: when a new session
// claims a pane already bound to another live session, the displaced
// row is marked ended AND its process is NULL'd, so a future tmux
// pane-id reuse can't pull it back into a lookup.
func TestTouchSession_NullsCollidedRowProcess(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	// Pre-existing session on the pane.
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, '2026-05-20T00:00:00')`,
		"sess-collide-old", 1, fakePane,
	); err != nil {
		t.Fatalf("seed old: %v", err)
	}

	// New session reclaims the same pane.
	if err := TouchSession("sess-collide-new", "claude", fakePane, 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	var process *string
	var state string
	if err := db.QueryRow(
		"SELECT process, state FROM sessions WHERE session_id = ?", "sess-collide-old",
	).Scan(&process, &state); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "ended" {
		t.Errorf("displaced state = %q, want ended", state)
	}
	if process != nil {
		t.Errorf("displaced process = %q, want NULL", *process)
	}
}
