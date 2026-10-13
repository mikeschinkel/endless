package monitor

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// E-2228: what an idle `session monitor` tick costs. These pin the three
// per-tick costs this package removed: the index rewrite a read-only status
// caused, the second status a frame ran on its focal worktree, and the two tmux
// execs liveness spent on every dead socket.

func TestGitArgv_ReadsWithoutOptionalLocks(t *testing.T) {
	got := gitArgv("/wt", "status", "--porcelain")
	want := []string{"--no-optional-locks", "-C", "/wt", "status", "--porcelain"}
	if len(got) != len(want) {
		t.Fatalf("gitArgv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gitArgv = %q, want %q", got, want)
		}
	}
}

// countStatus installs a runGit stub that answers every probe cleanly and
// counts the `status` calls per directory.
func countStatus(t *testing.T) map[string]int {
	t.Helper()
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	t.Cleanup(resetDefaultBranchCache)
	resetDefaultBranchCache()
	calls := map[string]int{}
	runGit = func(_ context.Context, dir string, args ...string) (string, error) {
		switch args[0] {
		case "status":
			calls[dir]++
			return "", nil
		case "symbolic-ref":
			return "main", nil
		case "rev-parse":
			return t.TempDir(), nil
		}
		return "", nil
	}
	return calls
}

func TestFrameMemo_OneStatusPerWorktreePerFrame(t *testing.T) {
	calls := countStatus(t)
	wt := "/wt-memo"

	ctx := WithFrameMemo(context.Background())
	worktreeUnsettledAt(ctx, wt, unlandedCacheOnly, false)
	worktreeAnomaliesAt(ctx, "", wt)
	if calls[wt] != 1 {
		t.Fatalf("status ran %d times in one frame, want 1", calls[wt])
	}

	// The next frame is a new memo, and sees the worktree as it is now.
	next := WithFrameMemo(context.Background())
	worktreeAnomaliesAt(next, "", wt)
	if calls[wt] != 2 {
		t.Fatalf("status ran %d times across two frames, want 2", calls[wt])
	}
}

func TestFrameMemo_AbsentMeansEveryCallRuns(t *testing.T) {
	calls := countStatus(t)
	wt := "/wt-nomemo"

	worktreeUnsettledAt(context.Background(), wt, unlandedCacheOnly, false)
	worktreeAnomaliesAt(context.Background(), "", wt)
	if calls[wt] != 2 {
		t.Fatalf("status ran %d times without a memo, want 2", calls[wt])
	}
}

// shortSocketDir returns a directory whose paths fit a unix socket's ~104-byte
// limit, which t.TempDir's often do not on macOS.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "e2228")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestSocketAccepts_ListeningServer(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "live")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	if !socketAccepts(path) {
		t.Fatal("socketAccepts = false for a listening socket")
	}
}

// A server killed without unlinking its socket leaves exactly this: a socket
// file nothing listens on.
func TestSocketAccepts_DeadSocketFile(t *testing.T) {
	path := filepath.Join(shortSocketDir(t), "dead")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("expected a leftover socket file at %s (err %v)", path, err)
	}
	if socketAccepts(path) {
		t.Fatal("socketAccepts = true for a socket nothing listens on")
	}
}
