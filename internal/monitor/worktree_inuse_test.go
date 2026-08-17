package monitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// installLiveStub swaps the lsof probe for a deterministic answer.
func installLiveStub(t *testing.T, live bool, err error) {
	t.Helper()
	prev := hasLiveProcessInDir
	hasLiveProcessInDir = func(string) (bool, error) { return live, err }
	t.Cleanup(func() { hasLiveProcessInDir = prev })
}

func TestWorktreeInUse_ActiveSessionIsInUse(t *testing.T) {
	db := newReaperTestDB(t)
	installLiveStub(t, false, nil)
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, task_id)
		 VALUES (9, 'sess-9', 1, 'working', 42)`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	inUse, reason, err := WorktreeInUse(db, t.TempDir(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inUse {
		t.Errorf("expected in use with a working session bound to the task")
	}
	if reason != ReasonActiveSession {
		t.Errorf("reason = %q, want %q", reason, ReasonActiveSession)
	}
}

// The session probe is the half lsof cannot do: a bound session whose cwd has
// stepped OUT of the directory leaves no process standing in it.
func TestWorktreeInUse_ActiveSessionWithNoProcessInDir(t *testing.T) {
	db := newReaperTestDB(t)
	installLiveStub(t, false, nil)
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, task_id)
		 VALUES (9, 'sess-9', 1, 'idle', 42)`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	inUse, reason, err := WorktreeInUse(db, t.TempDir(), 42)
	if err != nil || !inUse || reason != ReasonActiveSession {
		t.Errorf("got (%v, %q, %v), want (true, %q, nil)",
			inUse, reason, err, ReasonActiveSession)
	}
}

func TestWorktreeInUse_EndedSessionIsNotInUse(t *testing.T) {
	db := newReaperTestDB(t)
	installLiveStub(t, false, nil)
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, task_id)
		 VALUES (9, 'sess-9', 1, 'ended', 42)`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	inUse, reason, err := WorktreeInUse(db, t.TempDir(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inUse {
		t.Errorf("expected not in use, got in use (%q)", reason)
	}
}

// The live-process probe is the half the sessions table cannot do: any
// process, Claude or not, standing in the directory.
func TestWorktreeInUse_LiveProcessIsInUse(t *testing.T) {
	db := newReaperTestDB(t)
	installLiveStub(t, true, nil)
	inUse, reason, err := WorktreeInUse(db, t.TempDir(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inUse || reason != ReasonLiveProcess {
		t.Errorf("got (%v, %q), want (true, %q)", inUse, reason, ReasonLiveProcess)
	}
}

func TestWorktreeInUse_IdleWorktreeIsNotInUse(t *testing.T) {
	db := newReaperTestDB(t)
	installLiveStub(t, false, nil)
	inUse, reason, err := WorktreeInUse(db, t.TempDir(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inUse || reason != ReasonNone {
		t.Errorf("got (%v, %q), want (false, %q)", inUse, reason, ReasonNone)
	}
}

// taskID 0 means "no owning task" — the sessions probe has nothing to look up
// and must be skipped, leaving the live-process probe as the whole answer.
func TestWorktreeInUse_ZeroTaskSkipsSessionProbe(t *testing.T) {
	db := newReaperTestDB(t)
	installLiveStub(t, false, nil)
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, task_id)
		 VALUES (9, 'sess-9', 1, 'working', 42)`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	inUse, _, err := WorktreeInUse(db, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inUse {
		t.Errorf("expected the session probe to be skipped for task 0")
	}
}

// Fail closed on BOTH probes: a caller about to remove a directory must read
// "could not tell" as "do not remove".
func TestWorktreeInUse_SessionQueryErrorFailsClosed(t *testing.T) {
	db := newReaperTestDB(t)
	installLiveStub(t, false, nil)
	db.Close()

	inUse, reason, err := WorktreeInUse(db, t.TempDir(), 42)
	if err == nil {
		t.Fatalf("expected an error from a closed DB")
	}
	if !inUse || reason != ReasonUndetermined {
		t.Errorf("got (%v, %q), want (true, %q)", inUse, reason, ReasonUndetermined)
	}
}

func TestWorktreeInUse_LiveProbeErrorFailsClosed(t *testing.T) {
	db := newReaperTestDB(t)
	installLiveStub(t, false, os.ErrNotExist)

	inUse, reason, err := WorktreeInUse(db, t.TempDir(), 42)
	if err == nil {
		t.Fatalf("expected the live-probe error to propagate")
	}
	if !inUse || reason != ReasonUndetermined {
		t.Errorf("got (%v, %q), want (true, %q)", inUse, reason, ReasonUndetermined)
	}
}

// ── the real lsof probe ────────────────────────────────────────────────────
//
// The stubs above cannot catch the defect E-1947 actually found: the probe
// itself answered "no process here" for a directory a process was
// demonstrably sitting in, because lsof ORs its selection criteria without
// `-a` and exits 1 even when it DID match. These two exercise the real
// command against a real process.

func TestRealHasLiveProcessInDir_SeesAProcessStandingInIt(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not available")
	}
	// Nest the target the way a real worktree is nested; the reproduction was
	// path-shaped enough that a bare tmpdir did not show it.
	dir := filepath.Join(t.TempDir(), "proj", ".endless", "worktrees", "e-42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cmd := exec.Command("sleep", "60")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	live, err := realHasLiveProcessInDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !live {
		t.Errorf("expected live=true for a directory holding a live process's cwd")
	}
}

func TestRealHasLiveProcessInDir_EmptyDirIsNotLive(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not available")
	}
	dir := filepath.Join(t.TempDir(), "proj", ".endless", "worktrees", "e-43")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	live, err := realHasLiveProcessInDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if live {
		t.Errorf("expected live=false for a directory nothing is standing in")
	}
}
