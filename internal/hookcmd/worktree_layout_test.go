package hookcmd

import (
	"os"
	"path/filepath"
	"testing"
)

// Shared fixtures for the hookcmd tests that need a worktree on disk. They
// lived in claude_skip_test.go until E-2166 deleted that suite along with the
// per-worktree hook pin it covered; three other suites still use them, so they
// got a file of their own rather than a home inside whichever test happened to
// be first.

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// makeWorktreeLayout builds a project root containing one endless-managed task
// worktree and returns both paths.
func makeWorktreeLayout(t *testing.T) (projectRoot, worktreeRoot string) {
	t.Helper()
	projectRoot = t.TempDir()
	worktreeRoot = filepath.Join(projectRoot, ".endless", "worktrees", "e-test")
	writeTestFile(t, filepath.Join(worktreeRoot, ".endless", "worktree.json"), `{}`)
	return projectRoot, worktreeRoot
}
