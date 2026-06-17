package monitor

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// GetTaskTitle returns the title of the given task, or empty string if not found.
func GetTaskTitle(taskID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var title sql.NullString
	err = db.QueryRow(`SELECT title FROM tasks WHERE id=?`, taskID).Scan(&title)
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
	err = db.QueryRow(`SELECT status FROM tasks WHERE id=?`, taskID).Scan(&status)
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
	switch status {
	case "confirmed", "assumed", "declined", "obsolete", "completed":
		return true
	}
	return false
}

// RegisterTaskFile records that a file was edited under the given task.
// Idempotent: subsequent edits to the same (task_id, file_path) are no-ops.
func RegisterTaskFile(taskID int64, sessionID, filePath string) error {
	if taskID == 0 || filePath == "" {
		return nil
	}
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		`INSERT OR IGNORE INTO task_files (task_id, file_path, first_edited_session_id)
		 VALUES (?, ?, ?)`,
		taskID, filePath, sessionID,
	)
	if err != nil {
		return fmt.Errorf("registering task file: %w", err)
	}
	return nil
}

// IsFileInTaskScope returns true if the file path is registered to the task,
// or appears literally in the task's title, description, or text fields.
// Uses substring matching against the absolute file path so both basename
// and project-relative path mentions match.
func IsFileInTaskScope(taskID int64, filePath string) (bool, error) {
	if taskID == 0 || filePath == "" {
		return false, nil
	}
	db, err := DB()
	if err != nil {
		return false, err
	}

	var exists int
	err = db.QueryRow(
		`SELECT 1 FROM task_files WHERE task_id=? AND file_path=? LIMIT 1`,
		taskID, filePath,
	).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if err != sql.ErrNoRows {
		return false, fmt.Errorf("querying task_files: %w", err)
	}

	var title, desc, text sql.NullString
	err = db.QueryRow(
		`SELECT title, description, text FROM tasks WHERE id=?`,
		taskID,
	).Scan(&title, &desc, &text)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("loading task %d: %w", taskID, err)
	}

	base := filepath.Base(filePath)
	for _, hay := range []string{title.String, desc.String, text.String} {
		if hay == "" {
			continue
		}
		if strings.Contains(hay, filePath) || strings.Contains(hay, base) {
			return true, nil
		}
	}
	return false, nil
}
