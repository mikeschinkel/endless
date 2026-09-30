package monitor

import (
	"database/sql"
	"errors"
	"fmt"
)

// The exported reads below back `endless-go resume-windows` (E-2196), which
// recovers every task window a tmux crash restored. Each wraps an existing
// package-private query rather than restating it, so the recovery sweep asks
// exactly the questions the status line and `session status` already ask.

// IsShellCommand reports whether a tmux pane_current_command value is an
// interactive shell — the pane is at a prompt, so text typed into it runs as a
// command. See isShellCommand for why the test is framed this way round.
func IsShellCommand(cmd string) bool {
	return isShellCommand(cmd)
}

// LiveSessionForPanes returns the most-recently-active live session bound to
// any of panes on the tmux server this process reaches, or 0 when none is.
// Server-scoped through ProcessIDsForPanes, so a session bound to the same
// "%N" on a server that died in a crash never matches a restored pane.
func LiveSessionForPanes(panes []string) (int64, error) {
	return sessionForPanes(panes)
}

// TaskProjectPath returns the resolved root of the project that owns taskID,
// or "" when the task or its project row does not exist.
func TaskProjectPath(taskID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var projectID int64
	err = db.QueryRow(
		`SELECT COALESCE(project_id, 0) FROM live_tasks WHERE id = ?`, taskID,
	).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && projectID == 0) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read project of E-%d: %w", taskID, err)
	}
	path, err := ProjectPath(projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return path, err
}
