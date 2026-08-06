package channelcmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newWorktreeDir builds a realistic <root>/.endless/worktrees/<name> path.
func newWorktreeDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".endless", "worktrees", name)
	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

// TestWatchWorktreeRemovalFiresOnRemoval is the E-1904 regression: a channel
// process must not outlive the worktree it holds open.
func TestWatchWorktreeRemovalFiresOnRemoval(t *testing.T) {
	dir := newWorktreeDir(t, "e-42")

	gone := watchWorktreeRemoval(dir, 10*time.Millisecond)
	if gone == nil {
		t.Fatal("a real worktree dir must be watched")
	}

	select {
	case <-gone:
		t.Fatal("fired while the worktree still exists")
	case <-time.After(50 * time.Millisecond):
	}

	err := os.RemoveAll(dir)
	if err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	select {
	case <-gone:
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not fire after the worktree was removed")
	}
}

// TestWatchWorktreeRemovalIgnoresNonWorktrees pins the nil-channel contract: a
// process outside a worktree keeps its original lifecycle, since a nil channel
// blocks forever in select.
func TestWatchWorktreeRemovalIgnoresNonWorktrees(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"missing":          filepath.Join(t.TempDir(), ".endless", "worktrees", "e-9"),
		"main_checkout":    t.TempDir(),
		"wrong_parent_dir": filepath.Join(t.TempDir(), "e-42"),
	}
	for name, dir := range cases {
		t.Run(name, func(t *testing.T) {
			if dir != "" && name == "wrong_parent_dir" {
				err := os.MkdirAll(dir, 0o755)
				if err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			}
			if got := watchWorktreeRemoval(dir, time.Millisecond); got != nil {
				t.Fatalf("expected nil channel for %q", dir)
			}
		})
	}
}
