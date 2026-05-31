package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorktreePathForTask_FindsBareDir pins the canonical-name branch:
// a directory named `e-<task_id>` under .endless/worktrees resolves to
// the full absolute path.
func TestWorktreePathForTask_FindsBareDir(t *testing.T) {
	db := withTestDB(t)
	projectRoot := t.TempDir()
	seedProject(t, db, 1, "acme", projectRoot)

	wantWT := filepath.Join(projectRoot, ".endless", "worktrees", "e-808")
	if err := os.MkdirAll(wantWT, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := WorktreePathForTask(1, 808)
	if err != nil {
		t.Fatalf("WorktreePathForTask: %v", err)
	}
	if got != wantWT {
		t.Errorf("got %q, want %q", got, wantWT)
	}
}

// TestWorktreePathForTask_FindsSluggedDir pins the slugged-name branch:
// a directory named `e-<task_id>-<slug>` also matches; the function
// trusts the path prefix rather than requiring a bare name.
func TestWorktreePathForTask_FindsSluggedDir(t *testing.T) {
	db := withTestDB(t)
	projectRoot := t.TempDir()
	seedProject(t, db, 1, "acme", projectRoot)

	wantWT := filepath.Join(projectRoot, ".endless", "worktrees", "e-808-event-logs")
	if err := os.MkdirAll(wantWT, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := WorktreePathForTask(1, 808)
	if err != nil {
		t.Fatalf("WorktreePathForTask: %v", err)
	}
	if got != wantWT {
		t.Errorf("got %q, want %q", got, wantWT)
	}
}

// TestWorktreePathForTask_NoMatchReturnsEmpty pins the negative branch:
// no directory under .endless/worktrees matches the task id (including
// numeric prefix near-misses like e-8080 for task 808), so the result is
// "" with nil error.
func TestWorktreePathForTask_NoMatchReturnsEmpty(t *testing.T) {
	db := withTestDB(t)
	projectRoot := t.TempDir()
	seedProject(t, db, 1, "acme", projectRoot)

	// Near-miss: e-8080 shares the "e-808" prefix but is task 8080, not 808.
	if err := os.MkdirAll(
		filepath.Join(projectRoot, ".endless", "worktrees", "e-8080"),
		0755,
	); err != nil {
		t.Fatalf("mkdir near-miss: %v", err)
	}

	got, err := WorktreePathForTask(1, 808)
	if err != nil {
		t.Fatalf("WorktreePathForTask: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want \"\" (near-miss prefix must not match)", got)
	}
}

// TestWorktreePathForTask_ZeroTaskIDReturnsEmpty pins the input guard:
// task id ≤ 0 short-circuits to "" without filesystem work, so callers
// can fire unconditionally.
func TestWorktreePathForTask_ZeroTaskIDReturnsEmpty(t *testing.T) {
	db := withTestDB(t)
	projectRoot := t.TempDir()
	seedProject(t, db, 1, "acme", projectRoot)

	got, err := WorktreePathForTask(1, 0)
	if err != nil {
		t.Fatalf("WorktreePathForTask: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want \"\" for zero taskID", got)
	}
}
