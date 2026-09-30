package monitor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// E-2164 — the changed-path cache behind detected conflicts, against real git.

func refreshPaths(t *testing.T, root string, open ...int64) {
	t.Helper()
	resetDefaultBranchCache()
	set := map[int64]bool{}
	for _, id := range open {
		set[id] = true
	}
	if err := RefreshWorktreePathsCache(context.Background(), root, set); err != nil {
		t.Fatalf("RefreshWorktreePathsCache: %v", err)
	}
}

// forbidGit fails the test on ANY git subprocess for the rest of it: the
// render-path reader must answer from files alone.
func forbidGit(t *testing.T) {
	t.Helper()
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
		t.Fatalf("render path ran git %v in %s", args, dir)
		return "", nil
	}
}

func TestWorktreeChangedPaths_EmptyCacheIsAMissWithoutGit(t *testing.T) {
	f := newCacheFixture(t, 1)
	forbidGit(t)
	if paths, ok := WorktreeChangedPaths(f.root, 2129); ok || paths != nil {
		t.Fatalf("before any pass: (%v, %v), want a miss", paths, ok)
	}
}

func TestWorktreeChangedPaths_CommittedAndUncommittedPaths(t *testing.T) {
	f := newCacheFixture(t, 2)
	// Uncommitted edit, an untracked file, and an Endless-written path that
	// must not count.
	if err := os.WriteFile(filepath.Join(f.worktrees[0], "f.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.worktrees[0], "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.worktrees[0], ".endless", "db-ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.worktrees[0], ".endless", "db-ledger", "x.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.commitIn(t, f.worktrees[0], ".endless-hooks-like.sh", "a project file")

	refreshPaths(t, f.root, 2129) // 2130 is not open: skipped

	forbidGit(t)
	got, ok := WorktreeChangedPaths(f.root, 2129)
	if !ok {
		t.Fatalf("no current entry after a pass")
	}
	want := []string{".endless-hooks-like.sh", "f.txt", "new.txt", "work.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if _, ok := WorktreeChangedPaths(f.root, 2130); ok {
		t.Errorf("a task that is not open must have no entry")
	}
}

func TestWorktreeChangedPaths_MovedHeadIsStale(t *testing.T) {
	f := newCacheFixture(t, 1)
	refreshPaths(t, f.root, 2129)
	if _, ok := WorktreeChangedPaths(f.root, 2129); !ok {
		t.Fatalf("no entry after a pass")
	}
	f.commitIn(t, f.worktrees[0], "more.txt", "more work")
	if _, ok := WorktreeChangedPaths(f.root, 2129); ok {
		t.Fatalf("an entry computed at an older HEAD must read as a miss")
	}
	refreshPaths(t, f.root, 2129)
	got, ok := WorktreeChangedPaths(f.root, 2129)
	if !ok || !reflect.DeepEqual(got, []string{"more.txt", "work.txt"}) {
		t.Fatalf("after re-pass: (%v, %v)", got, ok)
	}
}

func TestRefreshWorktreePathsCache_PrunesFinishedTasks(t *testing.T) {
	f := newCacheFixture(t, 1)
	refreshPaths(t, f.root, 2129)
	refreshPaths(t, f.root) // the task finished
	if _, ok := WorktreeChangedPaths(f.root, 2129); ok {
		t.Fatalf("a finished task's entry must be pruned")
	}
}

func TestPorcelainZPaths_RenameCountsBothEnds(t *testing.T) {
	got := porcelainZPaths("R  new.go\x00old.go\x00 M a.go\x00?? b.go\x00")
	want := []string{"new.go", "old.go", "a.go", "b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestIsExcludedChangedPath(t *testing.T) {
	for p, want := range map[string]bool{
		".endless/db-ledger/a.jsonl":   true,
		".endless/tasks/e-1/verify.sh": true,
		".endless/decisions/ED-1.md":   true,
		".endless/verbs.jsonl":         true,
		".endless/LESSONS.md":          true,
		".endless/hooks/post-land":     false,
		".endless/config.json":         false,
		".endless/migrations/x.go":     false,
		"src/x.go":                     false,
	} {
		if got := isExcludedChangedPath(p); got != want {
			t.Errorf("isExcludedChangedPath(%q) = %v, want %v", p, got, want)
		}
	}
}
