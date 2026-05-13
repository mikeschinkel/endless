package main

import (
	"path/filepath"
	"testing"
)

// resolveCwdTaskID is pure filesystem — these tests cover the
// no-worktree, missing-companion, malformed-id, and happy-path cases.
// The DB-touching `autoBindFromCwd` wrapper is covered by smoke tests
// rather than unit tests (no in-process test DB infra exists today).

func TestResolveCwdTaskID_CwdInMainCheckout(t *testing.T) {
	projectRoot, _ := makeWorktreeLayout(t)
	if got := resolveCwdTaskID(projectRoot, projectRoot); got != 0 {
		t.Fatalf("expected 0 for cwd in main checkout (no companion); got %d", got)
	}
}

func TestResolveCwdTaskID_CwdInWorktreeNoTaskID(t *testing.T) {
	projectRoot, worktreeRoot := makeWorktreeLayout(t)
	// makeWorktreeLayout writes companion `{}` — no task_id field.
	if got := resolveCwdTaskID(projectRoot, worktreeRoot); got != 0 {
		t.Fatalf("expected 0 for companion with empty task_id; got %d", got)
	}
}

func TestResolveCwdTaskID_CwdInWorktreeMalformedTaskID(t *testing.T) {
	projectRoot := t.TempDir()
	worktreeRoot := filepath.Join(projectRoot, ".endless", "worktrees", "e-1291")
	writeTestFile(t, filepath.Join(worktreeRoot, ".endless", "worktree.json"),
		`{"task_id":"not-a-task-id"}`)
	if got := resolveCwdTaskID(projectRoot, worktreeRoot); got != 0 {
		t.Fatalf("expected 0 for malformed task_id; got %d", got)
	}
}

func TestResolveCwdTaskID_HappyPath(t *testing.T) {
	projectRoot := t.TempDir()
	worktreeRoot := filepath.Join(projectRoot, ".endless", "worktrees", "e-1291")
	writeTestFile(t, filepath.Join(worktreeRoot, ".endless", "worktree.json"),
		`{"task_id":"E-1291"}`)
	if got := resolveCwdTaskID(projectRoot, worktreeRoot); got != 1291 {
		t.Fatalf("expected 1291; got %d", got)
	}
}

func TestResolveCwdTaskID_CwdNestedUnderWorktree(t *testing.T) {
	projectRoot := t.TempDir()
	worktreeRoot := filepath.Join(projectRoot, ".endless", "worktrees", "e-1291")
	writeTestFile(t, filepath.Join(worktreeRoot, ".endless", "worktree.json"),
		`{"task_id":"E-1291"}`)
	deepCwd := filepath.Join(worktreeRoot, "internal", "monitor")
	writeTestFile(t, filepath.Join(deepCwd, ".keep"), "")
	if got := resolveCwdTaskID(projectRoot, deepCwd); got != 1291 {
		t.Fatalf("expected 1291 (walk-up resolution); got %d", got)
	}
}

func TestResolveCwdTaskID_CwdOutsideProject(t *testing.T) {
	projectRoot, _ := makeWorktreeLayout(t)
	otherDir := t.TempDir() // unrelated tempdir, not under projectRoot
	if got := resolveCwdTaskID(projectRoot, otherDir); got != 0 {
		t.Fatalf("expected 0 for cwd outside projectRoot; got %d", got)
	}
}
