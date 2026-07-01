package hookcmd

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

// stubSpawnMarkers points the tmux marker readers at fixed values for the
// duration of the test, simulating what the SessionStart hook reads from the
// tmux window. Restores the real readers on cleanup. Tests using it must NOT
// call t.Parallel() (the readers are package-global).
func stubSpawnMarkers(t *testing.T, spawnedBy string, taskID int64) {
	t.Helper()
	prevSpawnedBy, prevTaskID := tmuxSpawnedBy, tmuxTaskID
	tmuxSpawnedBy = func() string { return spawnedBy }
	tmuxTaskID = func() int64 { return taskID }
	t.Cleanup(func() {
		tmuxSpawnedBy = prevSpawnedBy
		tmuxTaskID = prevTaskID
	})
}

// TestTrySpawnBind_RaceReturnsFalse pins the SessionStart marker-read race
// (E-1700): a spawned window carries @endless_spawned_by, but the
// @endless_task_id read comes back empty (tmuxTaskID() == 0). trySpawnBind must
// report "not bound" so the caller falls back to cwd-derived binding. It returns
// before any DB call, so no DB fixture is needed.
func TestTrySpawnBind_RaceReturnsFalse(t *testing.T) {
	stubSpawnMarkers(t, "835", 0)
	if trySpawnBind(1, claudePayload{SessionID: "sess-race"}) {
		t.Fatal("trySpawnBind returned true on a taskID=0 marker race; want false (→ cwd fallback)")
	}
}

// TestSessionStartBind_CwdFallbackOnSpawnMarkerRace reproduces the E-1700 bug
// and verifies the fix end to end. A spawned worker's SessionStart hook sees the
// @endless_spawned_by marker but the @endless_task_id read races to empty, so the
// spawn-marker bind no-ops. The cwd fallback — now gated on !spawnBound rather
// than "no spawn marker" — must still bind the session to the task its worktree
// path encodes, so active_task_id is set (not NULL) and the status line shows
// the task instead of "claim a task".
//
// Against the pre-fix gate (tmuxSpawnedBy() == "") the same assertion fails:
// the fallback is skipped and active_task_id stays NULL.
func TestSessionStartBind_CwdFallbackOnSpawnMarkerRace(t *testing.T) {
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
	if _, err = db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)

	// A worktree layout whose path encodes task 1699, under a project root the
	// seeded projects row points at (so monitor.ProjectPath resolves it and the
	// cwd fallback finds the worktree).
	projectRoot := t.TempDir()
	worktreeRoot := filepath.Join(projectRoot, ".endless", "worktrees", "e-1699")
	writeTestFile(t, filepath.Join(worktreeRoot, ".endless", "worktree.json"), `{"task_id":"E-1699"}`)

	if _, err = db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', ?)", projectRoot); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err = db.Exec("INSERT INTO tasks (id, project_id, title, status) VALUES (1699, 1, 't', 'underway')"); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// Simulate the race: spawn marker readable, @endless_task_id not.
	stubSpawnMarkers(t, "835", 0)
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("TMUX_PANE", "")

	payload := claudePayload{SessionID: "sess-845", CWD: worktreeRoot}

	// Seed the NULL-task session row the way production does: TouchSession runs
	// at the top of runClaude before the bind decision. This makes the pre-fix
	// symptom the real one (active_task_id NULL on an existing row), not a
	// missing row.
	if err = monitor.TouchSession(payload.SessionID, "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	// Drive the real SessionStart bind decision: trySpawnBind reports the race
	// no-op, then maybeCwdBind (the production gate) recovers via cwd. Calling
	// the actual functions means reverting the gate breaks this test — it is not
	// a copy of the logic.
	spawnBound := trySpawnBind(1, payload)
	maybeCwdBind(1, payload, spawnBound)

	var activeTaskID *int64
	if err = db.QueryRow(
		"SELECT active_task_id FROM sessions WHERE session_id='sess-845'",
	).Scan(&activeTaskID); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if activeTaskID == nil {
		t.Fatal("active_task_id is NULL — spawned session lost its task (E-1700 bug reproduced)")
	}
	if *activeTaskID != 1699 {
		t.Fatalf("active_task_id = %d, want 1699", *activeTaskID)
	}
}
