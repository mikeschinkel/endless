package monitor

import (
	"database/sql"
	"fmt"

	"github.com/mikeschinkel/endless/internal/sessionstate"
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

// TaskClaim is a task's status and the session bound to it — the newest
// sessions row whose task_id is the task, the record `task show` reads its
// `Claimed:` line from. SessionID is 0 when no session was ever bound, and
// Status is empty when the task does not exist.
type TaskClaim struct {
	Status       string
	SessionID    int64
	SessionState string
}

// SessionLive reports whether the claiming session can still act. False when
// there is none.
func (c TaskClaim) SessionLive() bool {
	return c.SessionID != 0 && sessionstate.Has(sessionstate.Live, c.SessionState)
}

// GetTaskClaim reads a task's status and claiming session in one query.
func GetTaskClaim(taskID int64) (claim TaskClaim, err error) {
	db, err := DB()
	if err != nil {
		return claim, err
	}
	var sessionID sql.NullInt64
	var state sql.NullString
	err = db.QueryRow(
		`SELECT t.status, s.id, s.state
		   FROM live_tasks t
		   LEFT JOIN sessions s ON s.task_id = t.id
		  WHERE t.id = ?
		  ORDER BY s.id DESC
		  LIMIT 1`, taskID,
	).Scan(&claim.Status, &sessionID, &state)
	if err == sql.ErrNoRows {
		return TaskClaim{}, nil
	}
	if err != nil {
		return TaskClaim{}, fmt.Errorf("loading task claim: %w", err)
	}
	claim.SessionID = sessionID.Int64
	claim.SessionState = state.String
	return claim, nil
}
