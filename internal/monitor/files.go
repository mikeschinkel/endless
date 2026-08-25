package monitor

import (
	"database/sql"
	"fmt"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// GetTaskTitle returns the title of the given task, or empty string if not found.
func GetTaskTitle(taskID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var title sql.NullString
	err = db.QueryRow(`SELECT title FROM live_tasks WHERE id=?`, taskID).Scan(&title)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("loading task title: %w", err)
	}
	return title.String, nil
}

// GetTaskStatus returns the status of the given task, or empty string if not
// found.
func GetTaskStatus(taskID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var status sql.NullString
	err = db.QueryRow(`SELECT status FROM live_tasks WHERE id=?`, taskID).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("loading task status: %w", err)
	}
	return status.String, nil
}

// IsTerminalTaskStatus reports whether a status represents finished or abandoned
// work — confirmed, assumed, declined, obsolete, completed. These are tasks no
// longer actively worked (the first four also unblock dependents; see the
// blocker-filter set in tmux_lookup.go). The E-1586 cwd gate ignores them so a
// display-only bind of a done task, or a landed/retained worktree, never trips.
func IsTerminalTaskStatus(status string) bool {
	return taskstatus.Has(taskstatus.Terminal, status)
}
