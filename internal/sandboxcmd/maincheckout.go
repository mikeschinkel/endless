package sandboxcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MainCheckoutFromWorktree walks from a path inside a git worktree to the
// main checkout via the git-dir vs git-common-dir discriminator (also used in
// Go at internal/monitor/db.go, and by the `install` and `claude-settings-init`
// recipes in the justfile to refuse running from the wrong checkout).
// Exported for dbcmd's seed-sandbox, which seeds from the main checkout it names.
func MainCheckoutFromWorktree(dir string) (string, error) {
	gitDir, err := runGit(dir, "rev-parse", "--git-dir")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-dir in %s: %w", dir, err)
	}
	commonDir, err := runGit(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir in %s: %w", dir, err)
	}
	gitDirAbs, err := absPath(dir, gitDir)
	if err != nil {
		return "", err
	}
	commonAbs, err := absPath(dir, commonDir)
	if err != nil {
		return "", err
	}
	if gitDirAbs == commonAbs {
		return "", fmt.Errorf("cwd %s is the main checkout (or not a worktree); this needs a worktree", dir)
	}
	main := filepath.Dir(commonAbs)
	if _, err := os.Stat(main); err != nil {
		return "", fmt.Errorf("main checkout %s: %w", main, err)
	}
	return main, nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func absPath(base, p string) (string, error) {
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Abs(filepath.Join(base, p))
}
