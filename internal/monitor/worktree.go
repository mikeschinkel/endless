package monitor

import (
	"fmt"
	"os"
	"path/filepath"
)

// WorktreePathForTask returns the absolute path to the worktree associated
// with the given task, or "" if no such worktree exists. Convention:
// <project_root>/.endless/worktrees/e-<task_id>. Only the canonical bare
// `e-<task_id>` directory is recognized — one worktree per task by
// construction (ED-1515); named alternates are no longer tolerated.
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
	candidate := filepath.Join(
		projectRoot, ".endless", "worktrees", fmt.Sprintf("e-%d", taskID),
	)
	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat worktree: %w", err)
	}
	if !info.IsDir() {
		return "", nil
	}
	return candidate, nil
}
