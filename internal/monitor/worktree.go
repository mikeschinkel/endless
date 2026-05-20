package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WorktreePathForTask returns the absolute path to the worktree associated
// with the given task, or "" if no such worktree exists. Convention:
// <project_root>/.endless/worktrees/e-<task_id> or .../e-<task_id>-<slug>/.
// If multiple match, the lexicographically first directory wins.
func WorktreePathForTask(projectID int64, taskID int64) (string, error) {
	root, err := ProjectPath(projectID)
	if err != nil {
		return "", fmt.Errorf("project path: %w", err)
	}
	return worktreePathForTaskAtRoot(root, taskID)
}

func worktreePathForTaskAtRoot(projectRoot string, taskID int64) (string, error) {
	if taskID <= 0 {
		return "", nil
	}
	worktreesDir := filepath.Join(projectRoot, ".endless", "worktrees")
	prefix := fmt.Sprintf("e-%d", taskID)

	// Two patterns: bare 'e-<id>' and 'e-<id>-*'. Globs alone aren't strict
	// enough — 'e-1027*' also matches 'e-10270', so we filter post-glob.
	candidates := []string{filepath.Join(worktreesDir, prefix)}
	more, err := filepath.Glob(filepath.Join(worktreesDir, prefix+"-*"))
	if err != nil {
		return "", fmt.Errorf("glob worktrees: %w", err)
	}
	candidates = append(candidates, more...)
	sort.Strings(candidates)

	for _, c := range candidates {
		base := filepath.Base(c)
		if base != prefix && !strings.HasPrefix(base, prefix+"-") {
			continue
		}
		info, err := os.Stat(c)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return c, nil
		}
	}
	return "", nil
}
