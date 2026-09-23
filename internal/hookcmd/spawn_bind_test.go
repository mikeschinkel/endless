package hookcmd

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

// stubWindowTaskID points the @endless_task_id window-option reader at a fixed
// value for the duration of the test, simulating what a SessionStart reads in a
// tmux window `endless task spawn` created. Restores the real reader on cleanup.
// Tests using it must NOT call t.Parallel() (the reader is a package-global).
//
// There is no @endless_spawned_by stub any more: E-1983 removed hookcmd's reader
// for it along with the spawn-marker bind, since nothing in the hook needs to
// know whether a window was spawned once the window cannot decide a binding.
func stubWindowTaskID(t *testing.T, taskID int64) {
	t.Helper()
	prev := tmuxTaskID
	tmuxTaskID = func() int64 { return taskID }
	t.Cleanup(func() { tmuxTaskID = prev })
}

// TestLogWindowTaskDisagreement_DoesNotBind pins what is left of the spawn-marker
// path after E-1983: it observes, it never writes. A window option naming task
// 1732 and a session with a NULL task_id is the exact shape that used to produce
// a permanent mis-bind — the first write on a NULL row, which the write-once
// trigger (E-1969) permits and `task bind` can never undo.
func TestLogWindowTaskDisagreement_DoesNotBind(t *testing.T) {
	db := newBindTestDB(t)
	projectRoot := t.TempDir()
	seedProjectAndTasks(t, db, projectRoot, 1732)

	stubWindowTaskID(t, 1732)
	t.Setenv("TMUX_PANE", "")

	payload := claudePayload{SessionID: "sess-observe", CWD: projectRoot}
	if err := monitor.TouchSession(payload.SessionID, "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	logWindowTaskDisagreement(1, payload)

	if got := sessionTaskID(t, db, payload.SessionID); got != nil {
		t.Fatalf("task_id = %d, want NULL — the window-option observer must never "+
			"write sessions.task_id (E-1983)", *got)
	}
}

// TestTaskIDOrNone pins the log rendering of a resolveCwdTaskID result, where 0
// means "cwd is not inside a task worktree" rather than task zero.
func TestTaskIDOrNone(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "no task"},
		{-1, "no task"},
		{1699, "E-1699"},
	}
	for _, c := range cases {
		if got := taskIDOrNone(c.in); got != c.want {
			t.Errorf("taskIDOrNone(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSessionStartBind_SpawnedWorkerBindsFromCwd is the E-1700 case, re-pinned
// against the E-1983 bind path. A spawned worker's SessionStart used to bind
// from the window option, with cwd as the fallback for when that option raced to
// empty; cwd is now the only path, and it must still bind the worker the spawn
// launched. `task spawn` runs `tmux new-window -c <worktree>`, so the worker's
// cwd IS its worktree and the bind is reliable by construction — which is why
// removing the window-option bind costs the spawn flow nothing.
func TestSessionStartBind_SpawnedWorkerBindsFromCwd(t *testing.T) {
	// Fresh file-backed DB with the real schema, injected into the
	// monitor.DB() singleton via the exported test seam (E-1506).
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "endless.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("enable fks: %v", err)
	}
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)

	// A worktree layout whose path encodes task 1699, under a project root the
	// seeded projects row points at (so monitor.ProjectPath resolves it and the
	// cwd bind finds the worktree).
	projectRoot := t.TempDir()
	worktreeRoot := filepath.Join(projectRoot, ".endless", "worktrees", "e-1699")
	writeTestFile(t, filepath.Join(worktreeRoot, ".endless", "worktree.json"), `{"task_id":"E-1699"}`)

	if _, err = db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', ?)", projectRoot); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err = db.Exec("INSERT INTO tasks (id, project_id, title, status) VALUES (1699, 1, 't', 'underway')"); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// The E-1700 race: the window option reads empty.
	stubWindowTaskID(t, 0)
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("TMUX_PANE", "")

	payload := claudePayload{SessionID: "sess-845", CWD: worktreeRoot}

	// Seed the NULL-task session row the way production does: TouchSession runs
	// at the top of runClaude before the bind decision. This makes the pre-fix
	// symptom the real one (task_id NULL on an existing row), not a
	// missing row.
	if err = monitor.TouchSession(payload.SessionID, "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	// Drive the real SessionStart bind decision. Calling the actual function
	// means changing the gate breaks this test — it is not a copy of the logic.
	maybeCwdBind(1, payload)

	var taskID *int64
	if err = db.QueryRow(
		"SELECT task_id FROM sessions WHERE session_id='sess-845'",
	).Scan(&taskID); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if taskID == nil {
		t.Fatal("task_id is NULL — spawned session lost its task (E-1700 bug reproduced)")
	}
	if *taskID != 1699 {
		t.Fatalf("task_id = %d, want 1699", *taskID)
	}
}
