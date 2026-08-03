package hookcmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

// newBindTestDB opens a fresh file-backed DB with the real schema and injects it
// into the monitor.DB() singleton via the exported test seam (E-1506). Returns
// the handle so tests can seed rows and assert on them directly.
func newBindTestDB(t *testing.T) *sql.DB {
	t.Helper()
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
	return db
}

// seedWorktree writes the companion file for a task worktree under projectRoot
// and returns its absolute path. The path encodes the task id (E-1301), which is
// what resolveCwdTaskID reads.
func seedWorktree(t *testing.T, projectRoot string, taskID int) string {
	t.Helper()
	wt := filepath.Join(projectRoot, ".endless", "worktrees", "e-"+strconv.Itoa(taskID))
	writeTestFile(t, filepath.Join(wt, ".endless", "worktree.json"), "{}")
	return wt
}

// TestAutoBindFromCwd_ResumeDoesNotRebindDifferentTask reproduces the E-1856
// incident: a resume (`claude --resume <uuid>`) fires SessionStart with the
// session's existing active_task_id intact, but its cwd is a DIFFERENT task's
// worktree. The pre-fix cwd auto-bind derives the task id from the cwd directory
// name and unconditionally overwrites active_task_id, silently repointing the
// session away from the task it belongs to — making it unreachable via
// `session goto`/`session resume` for that task.
//
// The auto-bind is a fallback to fill an UNBOUND session from its cwd, never a
// re-pointer. So a session already bound to task 1835 whose cwd is task 1832's
// worktree must keep active_task_id = 1835.
//
// Against the pre-fix code this fails: active_task_id comes back 1832.
func TestAutoBindFromCwd_ResumeDoesNotRebindDifferentTask(t *testing.T) {
	db := newBindTestDB(t)

	projectRoot := t.TempDir()
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', ?)", projectRoot); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec("INSERT INTO tasks (id, project_id, title, status) VALUES (1835, 1, 'own', 'underway')"); err != nil {
		t.Fatalf("seed task 1835: %v", err)
	}
	if _, err := db.Exec("INSERT INTO tasks (id, project_id, title, status) VALUES (1832, 1, 'other', 'underway')"); err != nil {
		t.Fatalf("seed task 1832: %v", err)
	}

	// The session belongs to task 1835 and is currently bound to it.
	if err := monitor.BindSessionToTask("sess-997", 1, 1835); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	// Its resumed process's cwd is task 1832's worktree.
	otherWorktree := seedWorktree(t, projectRoot, 1832)

	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("TMUX_PANE", "")

	payload := claudePayload{SessionID: "sess-997", CWD: otherWorktree}
	// No spawn marker on a resume, so spawnBound is false and the cwd path runs.
	maybeCwdBind(1, payload, false)

	var activeTaskID *int64
	if err := db.QueryRow(
		"SELECT active_task_id FROM sessions WHERE session_id='sess-997'",
	).Scan(&activeTaskID); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if activeTaskID == nil {
		t.Fatal("active_task_id became NULL — should have kept 1835")
	}
	if *activeTaskID != 1835 {
		t.Fatalf("active_task_id = %d, want 1835 (resume silently rebound to the cwd worktree's task — E-1856 bug)", *activeTaskID)
	}
}

// TestSessionStart_LiveOwnedWorktreeRefusesAndDoesNotBind covers E-1856
// behavior 1: when a session starts (or resumes) with its cwd inside a worktree
// already owned by a LIVE sibling session (a non-stale worktree lock held by a
// different session), the SessionStart flow must refuse with an actionable
// message and must NOT bind the incoming session to that task — never creating a
// phantom co-owner.
//
// This drives the real SessionStart sequence from runClaude: handleWorktreeAdoption
// first (which returns the refusal and short-circuits), then maybeCwdBind only if
// there was no refusal. The test asserts the two guarantees together — refusal
// text present AND the incoming session left unbound.
func TestSessionStart_LiveOwnedWorktreeRefusesAndDoesNotBind(t *testing.T) {
	db := newBindTestDB(t)

	projectRoot := t.TempDir()
	if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', ?)", projectRoot); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec("INSERT INTO tasks (id, project_id, title, status) VALUES (1832, 1, 'other', 'underway')"); err != nil {
		t.Fatalf("seed task 1832: %v", err)
	}

	// Task 1832's worktree, locked by a LIVE sibling session. PID = this test
	// process, so IsWorktreeLockStale reports the owner alive.
	worktree := seedWorktree(t, projectRoot, 1832)
	if err := monitor.ClaimWorktreeLock(worktree, monitor.WorktreeLock{
		SessionID: "sess-owner",
		PID:       os.Getpid(),
	}); err != nil {
		t.Fatalf("claim lock for live owner: %v", err)
	}

	// A fresh, unbound session lands in that worktree.
	if err := monitor.TouchSession("sess-intruder", "claude", "", 1); err != nil {
		t.Fatalf("seed intruder session: %v", err)
	}

	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("TMUX_PANE", "")

	payload := claudePayload{SessionID: "sess-intruder", CWD: worktree}

	// Real SessionStart sequence: adoption first, cwd-bind only if not refused.
	refusal, err := handleWorktreeAdoption(1, payload)
	if err != nil {
		t.Fatalf("handleWorktreeAdoption: %v", err)
	}
	if refusal == "" {
		t.Fatal("expected a refusal for a live-owned worktree; got none")
	}
	maybeCwdBind(1, payload, false)

	var activeTaskID *int64
	if err := db.QueryRow(
		"SELECT active_task_id FROM sessions WHERE session_id='sess-intruder'",
	).Scan(&activeTaskID); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if activeTaskID != nil {
		t.Fatalf("active_task_id = %d, want NULL — the intruder must not co-own a live-owned task (E-1856)", *activeTaskID)
	}
}
