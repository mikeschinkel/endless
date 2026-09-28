package verifycmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/go-dt"
)

func TestResetSandboxClearsTheWorktreeSandbox(t *testing.T) {
	worktree := filepath.Join(t.TempDir(), ".endless", "worktrees", "e-42")
	stale := filepath.Join(worktree, ".endless", "sandbox", "stale")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := resetSandbox(dt.DirPath(worktree)); err != nil {
		t.Fatalf("resetSandbox: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a verify run did not start from a fresh sandbox: %v", err)
	}
}

func TestResetSandboxOutsideWorktreeIsANoop(t *testing.T) {
	if err := resetSandbox(dt.DirPath(t.TempDir())); err != nil {
		t.Errorf("resetSandbox outside a worktree: %v", err)
	}
}
