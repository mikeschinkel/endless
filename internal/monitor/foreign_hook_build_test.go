package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

// newSelfDevWorktree builds <root>/.endless/{config.json, worktrees/e-777} and
// returns (worktreePath, expectedBinary). self_dev controls the config flag.
func newSelfDevWorktree(t *testing.T, selfDev bool) (wt, expectedBin string) {
	t.Helper()
	root := t.TempDir()
	endless := filepath.Join(root, ".endless")
	wt = filepath.Join(endless, "worktrees", "e-777")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"self_dev": false}`
	if selfDev {
		body = `{"self_dev": true}`
	}
	if err := os.WriteFile(filepath.Join(endless, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return wt, filepath.Join(wt, "bin", "endless-go")
}

func TestForeignHookBuild(t *testing.T) {
	t.Run("worktree's own binary running -> not foreign", func(t *testing.T) {
		wt, expected := newSelfDevWorktree(t, true)
		got, foreign := ForeignHookBuild(wt, expected)
		if foreign {
			t.Fatalf("running the worktree binary must not be foreign; got expected=%q", got)
		}
	})

	t.Run("foreign global build -> foreign, names worktree binary", func(t *testing.T) {
		wt, expected := newSelfDevWorktree(t, true)
		got, foreign := ForeignHookBuild(wt, "/usr/local/bin/endless-go")
		if !foreign {
			t.Fatal("the global/main build serving a self_dev worktree must be flagged foreign")
		}
		if got != expected {
			t.Fatalf("expected worktree binary %q, got %q", expected, got)
		}
	})

	t.Run("symlinked global pointing at the worktree binary -> not foreign", func(t *testing.T) {
		wt, expected := newSelfDevWorktree(t, true)
		if err := os.MkdirAll(filepath.Dir(expected), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(expected, []byte("#!/bin/true\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "endless-go")
		if err := os.Symlink(expected, link); err != nil {
			t.Fatal(err)
		}
		if _, foreign := ForeignHookBuild(wt, link); foreign {
			t.Fatal("a symlink resolving to the worktree binary must not be foreign")
		}
	})

	t.Run("not inside a worktree -> not foreign", func(t *testing.T) {
		if _, foreign := ForeignHookBuild(t.TempDir(), "/usr/local/bin/endless-go"); foreign {
			t.Fatal("outside a self_dev worktree nothing is foreign")
		}
	})

	t.Run("worktree without self_dev -> not foreign", func(t *testing.T) {
		wt, _ := newSelfDevWorktree(t, false)
		if _, foreign := ForeignHookBuild(wt, "/usr/local/bin/endless-go"); foreign {
			t.Fatal("a downstream (non self_dev) worktree must never be flagged")
		}
	})
}

func TestWorktreeHookBinary(t *testing.T) {
	wt, expected := newSelfDevWorktree(t, true)
	if got := WorktreeHookBinary(wt); got != expected {
		t.Fatalf("WorktreeHookBinary = %q, want %q", got, expected)
	}
	// A subdir inside the worktree still resolves to the same binary.
	if got := WorktreeHookBinary(filepath.Join(wt, "internal", "x")); got != expected {
		t.Fatalf("from subdir: WorktreeHookBinary = %q, want %q", got, expected)
	}
	// Non-self_dev worktree → "".
	wt2, _ := newSelfDevWorktree(t, false)
	if got := WorktreeHookBinary(wt2); got != "" {
		t.Fatalf("non self_dev worktree: WorktreeHookBinary = %q, want \"\"", got)
	}
}
