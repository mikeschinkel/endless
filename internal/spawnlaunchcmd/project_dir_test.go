package spawnlaunchcmd

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// gitInit builds a throwaway repo with one commit, so `git worktree add` has a
// commit to branch from. Skips the test when git isn't available.
func gitInit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	// macOS /var → /private/var: resolve up front so the paths this test
	// compares are the ones git will report.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", root, err)
	}
	root = resolved
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "-q", "--allow-empty", "-m", "root")
	return root
}

// TestProjectDirFor_Worktree is the case that matters: a spawned session works
// in a per-task worktree, but the monitor and shell panes must start in the MAIN
// checkout so endless routes them to the real ledger rather than the worktree
// sandbox.
func TestProjectDirFor_Worktree(t *testing.T) {
	main := gitInit(t)
	wt := filepath.Join(main, "wt", "e-1851")
	cmd := exec.Command("git", "worktree", "add", "-q", "-b", "task/1851", wt)
	cmd.Dir = main
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	if got := projectDirFor(wt); got != main {
		t.Fatalf("projectDirFor(worktree) = %q, want main checkout %q", got, main)
	}
}

// TestProjectDirFor_MainCheckout pins that a cwd already in the main checkout is
// returned unchanged — the git-dir/git-common-dir discriminator is equal there,
// so there is no worktree to walk out of.
func TestProjectDirFor_MainCheckout(t *testing.T) {
	main := gitInit(t)
	if got := projectDirFor(main); got != main {
		t.Fatalf("projectDirFor(main) = %q, want %q", got, main)
	}
}

// TestProjectDirFor_NotAGitRepo pins the fallback: a cwd git knows nothing about
// yields that cwd. The layout must build over a cwd question, never fail on one.
func TestProjectDirFor_NotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	if got := projectDirFor(dir); got != dir {
		t.Fatalf("projectDirFor(non-repo) = %q, want %q", got, dir)
	}
}

// TestProjectDirFor_Empty pins that an empty cwd (spawn-window called without
// --cwd) stays empty, so splitWindowArgs omits -c and the pane inherits.
func TestProjectDirFor_Empty(t *testing.T) {
	if got := projectDirFor(""); got != "" {
		t.Fatalf("projectDirFor(\"\") = %q, want empty", got)
	}
}
