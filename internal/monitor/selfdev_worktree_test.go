package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

// InSelfDevWorktree decides whether a surface that would otherwise pin the main
// DB should instead honor the per-worktree sandbox (E-698). Getting it wrong in
// either direction is costly: a false negative points candidate code at the
// developer's real ledger, and a false positive points a normal session at a
// sandbox that has none of its data.

// selfDevTree builds <root>/.endless/worktrees/e-<id>/ and writes the project's
// .endless/config.json with the given self_dev flag. Returns the worktree dir.
func selfDevTree(t *testing.T, selfDev bool) string {
	t.Helper()

	root := t.TempDir()
	worktree := filepath.Join(root, ".endless", "worktrees", "e-698")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("create worktree tree: %v", err)
	}

	config := `{"self_dev": false}`
	if selfDev {
		config = `{"self_dev": true}`
	}
	if err := os.WriteFile(filepath.Join(root, ".endless", "config.json"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	return worktree
}

// chdir moves into dir for the duration of one test.
func chdir(t *testing.T, dir string) {
	t.Helper()

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err = os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if cerr := os.Chdir(prev); cerr != nil {
			t.Errorf("restore cwd: %v", cerr)
		}
	})
}

func TestInSelfDevWorktree_TrueInsideASelfDevProjectsWorktree(t *testing.T) {
	chdir(t, selfDevTree(t, true))

	if !InSelfDevWorktree() {
		t.Error("InSelfDevWorktree() = false inside a self_dev project's task worktree, want true")
	}
}

func TestInSelfDevWorktree_FalseWhenTheProjectIsNotSelfDev(t *testing.T) {
	chdir(t, selfDevTree(t, false))

	// A downstream project that merely USES endless keeps its worktree work in
	// the real DB — that is real audit data, not pollution.
	if InSelfDevWorktree() {
		t.Error("InSelfDevWorktree() = true in a non-self_dev project's worktree, want false")
	}
}

func TestInSelfDevWorktree_FalseOutsideAnyWorktree(t *testing.T) {
	chdir(t, t.TempDir())

	if InSelfDevWorktree() {
		t.Error("InSelfDevWorktree() = true outside any worktree, want false")
	}
}

func TestInSelfDevWorktree_FalseForANonTaskWorktreeDirectory(t *testing.T) {
	root := t.TempDir()
	// The path marker is present but the directory is not an e-NNN task
	// worktree, so it must not be treated as one.
	scratch := filepath.Join(root, ".endless", "worktrees", "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatalf("create scratch tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".endless", "config.json"), []byte(`{"self_dev": true}`), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	chdir(t, scratch)

	if InSelfDevWorktree() {
		t.Error("InSelfDevWorktree() = true for a non-task worktree directory, want false")
	}
}
