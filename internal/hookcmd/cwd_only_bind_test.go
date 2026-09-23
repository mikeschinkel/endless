package hookcmd

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	_ "modernc.org/sqlite"
)

// seedProjectAndTasks seeds project 1 at projectRoot plus the given task ids,
// all `underway`. Companion to newBindTestDB / seedWorktree in
// resume_rebind_test.go.
func seedProjectAndTasks(t *testing.T, db *sql.DB, projectRoot string, taskIDs ...int64) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', ?)", projectRoot); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	for _, id := range taskIDs {
		if _, err := db.Exec(
			"INSERT INTO tasks (id, project_id, title, status) VALUES (?, 1, 't', 'underway')", id,
		); err != nil {
			t.Fatalf("seed task %d: %v", id, err)
		}
	}
}

// sessionTaskID reads sessions.task_id for the given session; nil means unbound.
func sessionTaskID(t *testing.T, db *sql.DB, sessionID string) *int64 {
	t.Helper()
	var taskID *int64
	if err := db.QueryRow(
		"SELECT task_id FROM sessions WHERE session_id = ?", sessionID,
	).Scan(&taskID); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	return taskID
}

// TestSessionStartBind_MainCheckoutStaysUnbound is the E-1983 reproduction, and
// the single most important check in this task: a FRESH Claude session launched
// in the MAIN CHECKOUT, inside a tmux window a previous `task spawn` left
// carrying @endless_task_id, must land UNBOUND.
//
// Before the fix trySpawnBind reads that stale window option and binds the
// session to the window's task. sessions.task_id is write-once (E-1969), so on a
// NULL row that wrong bind is the FIRST write, the trigger permits it, and
// `task bind` can never move it. This is the E-1732 shape: two `claude` launches
// in the main checkout both bound to a task whose worktree they never entered.
func TestSessionStartBind_MainCheckoutStaysUnbound(t *testing.T) {
	db := newBindTestDB(t)
	projectRoot := t.TempDir()
	seedProjectAndTasks(t, db, projectRoot, 1732)

	// The window still says task 1732 from a spawn whose session has ended.
	stubWindowTaskID(t, 1732)
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("TMUX_PANE", "")

	// A brand-new session, launched in the main checkout — not in any worktree.
	payload := claudePayload{SessionID: "sess-fresh", CWD: projectRoot}
	if err := monitor.TouchSession(payload.SessionID, "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	logWindowTaskDisagreement(1, payload)
	maybeCwdBind(1, payload)

	if got := sessionTaskID(t, db, payload.SessionID); got != nil {
		t.Fatalf("task_id = %d, want NULL — a fresh session in the main checkout "+
			"was mis-bound from a stale window option (E-1983 reproduced)", *got)
	}
}

// TestSessionStartBind_CwdWinsOverStaleWindowOption is the second E-1983 case: a
// fresh session launched in worktree e-1699 in a window whose @endless_task_id
// still says 1732. The working directory is the only thing that binds, so the
// session must bind to 1699, not to the window's stale claim.
func TestSessionStartBind_CwdWinsOverStaleWindowOption(t *testing.T) {
	db := newBindTestDB(t)
	projectRoot := t.TempDir()
	seedProjectAndTasks(t, db, projectRoot, 1699, 1732)
	worktreeRoot := seedWorktree(t, projectRoot, 1699)

	stubWindowTaskID(t, 1732)
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("TMUX_PANE", "")

	payload := claudePayload{SessionID: "sess-wt", CWD: worktreeRoot}
	if err := monitor.TouchSession(payload.SessionID, "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	logWindowTaskDisagreement(1, payload)
	maybeCwdBind(1, payload)

	got := sessionTaskID(t, db, payload.SessionID)
	if got == nil {
		t.Fatal("task_id is NULL — the cwd worktree should have bound this session")
	}
	if *got != 1699 {
		t.Fatalf("task_id = %d, want 1699 — the stale window option won over cwd "+
			"(E-1983 reproduced)", *got)
	}
}
