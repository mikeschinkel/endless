package monitor

import (
	"fmt"
	"os"
	"time"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/go-dt"
)

// SessionInfo represents an active AI coding session.
type SessionInfo struct {
	ID           int64
	SessionID    string
	ProjectID    int64
	ActiveTaskID *int64
	State        string
	LastActivity string
}

// StartWorkSession creates or updates a session linked to a specific task.
// Also marks the task as in_progress.
func StartWorkSession(sessionID string, projectID int64, taskID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	process := os.Getenv("TMUX_PANE")
	_, err = db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, process, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   state='working', active_task_id=?, last_activity=?, project_id=?,
		   process=COALESCE(NULLIF(?, ''), process)`,
		sessionID, projectID, taskID, process, now, now,
		taskID, now, projectID, process,
	)
	if err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}

	_, err = db.Exec(
		"UPDATE tasks SET status='in_progress' WHERE id=? AND status IN ('needs_plan','ready','blocked')",
		taskID,
	)
	return err
}

// StartChatSession creates a working session with no task (chat-only).
func StartChatSession(sessionID string, projectID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	process := os.Getenv("TMUX_PANE")

	_, err = db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, process, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'working', NULL, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   state='working', active_task_id=NULL, last_activity=?,
		   process=COALESCE(NULLIF(?, ''), process)`,
		sessionID, projectID, process, now, now,
		now, process,
	)
	return err
}

// InitSession creates a session with state='needs_input' on SessionStart.
func InitSession(sessionID string, projectID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	_, err = db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'needs_input', ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET last_activity=?`,
		sessionID, projectID, now, now,
		now,
	)
	return err
}

// GetActiveSession returns the session info if it exists and is in an active state.
func GetActiveSession(sessionID string) (*SessionInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	var s SessionInfo
	err = db.QueryRow(
		`SELECT id, session_id, COALESCE(project_id,0), active_task_id, state, COALESCE(last_activity,'')
		 FROM sessions WHERE session_id=?`,
		sessionID,
	).Scan(&s.ID, &s.SessionID, &s.ProjectID, &s.ActiveTaskID, &s.State, &s.LastActivity)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SetPlanFilePath records which plan file this session is editing.
func SetPlanFilePath(sessionID, filePath string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		"UPDATE sessions SET plan_file_path=? WHERE session_id=?",
		filePath, sessionID,
	)
	return err
}

// GetPlanFilePath returns the plan file path for a session, if set.
func GetPlanFilePath(sessionID string) string {
	db, err := DB()
	if err != nil {
		return ""
	}
	var path *string
	err = db.QueryRow(
		"SELECT plan_file_path FROM sessions WHERE session_id=?",
		sessionID,
	).Scan(&path)
	if err != nil || path == nil {
		return ""
	}
	return *path
}

// SetProcess records which process identifier this session is running in.
func SetProcess(sessionID, process string) error {
	if process == "" {
		return nil
	}
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		"UPDATE sessions SET process=? WHERE session_id=?",
		process, sessionID,
	)
	return err
}

// RegisterChannelPort upserts the channel plugin's HTTP port in the channels table.
// The process key is typically TMUX_PANE or another session-unique identifier.
func RegisterChannelPort(process string, port, pid int) error {
	db, err := DB()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		`INSERT INTO channels (process, port, pid, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(process) DO UPDATE SET port=?, pid=?, created_at=?`,
		process, port, pid, now,
		port, pid, now,
	)
	return err
}

// UnregisterChannelPort removes a channel port entry.
func UnregisterChannelPort(process string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec("DELETE FROM channels WHERE process=?", process)
	return err
}

// LookupChannelPort returns the HTTP port for a given process identifier.
// Returns 0 if not found.
func LookupChannelPort(process string) (int, int, error) {
	db, err := DB()
	if err != nil {
		return 0, 0, err
	}
	var port, pid int
	err = db.QueryRow(
		"SELECT port, pid FROM channels WHERE process=?",
		process,
	).Scan(&port, &pid)
	if err != nil {
		return 0, 0, err
	}
	return port, pid, nil
}

// BackfillProcess sets process only if it's currently NULL.
func BackfillProcess(sessionID, process string) error {
	if process == "" {
		return nil
	}
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		"UPDATE sessions SET process=? WHERE session_id=? AND process IS NULL",
		process, sessionID,
	)
	return err
}

// TouchSession updates last_activity timestamp.
func TouchSession(sessionID string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"UPDATE sessions SET last_activity=? WHERE session_id=?",
		now, sessionID,
	)
	return err
}

// CompleteTask marks a task as confirmed and clears the session's active task.
func CompleteTask(sessionID string, taskID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	// Mark task as confirmed
	_, err = db.Exec(
		"UPDATE tasks SET status='confirmed', completed_at=? WHERE id=?",
		now, taskID,
	)
	if err != nil {
		return err
	}

	// Clear active task, set state to idle
	_, err = db.Exec(
		"UPDATE sessions SET active_task_id=NULL, state='idle', last_activity=? WHERE session_id=?",
		now, sessionID,
	)
	return err
}

// IdleSession marks a session as idle (between turns, still alive).
func IdleSession(sessionID string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"UPDATE sessions SET state='idle', last_activity=? WHERE session_id=?",
		now, sessionID,
	)
	return err
}

// EndSession marks a session as ended.
func EndSession(sessionID string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"UPDATE sessions SET state='ended', last_activity=? WHERE session_id=?",
		now, sessionID,
	)
	return err
}

// IsSessionExpired returns true if the session's last activity is older than timeoutMinutes.
func IsSessionExpired(s *SessionInfo, timeoutMinutes int) bool {
	if s.LastActivity == "" {
		return true
	}
	t, err := time.Parse("2006-01-02T15:04:05", s.LastActivity)
	if err != nil {
		return true
	}
	return time.Since(t) > time.Duration(timeoutMinutes)*time.Minute
}

// GetTrackingMode returns the tracking enforcement level for a project.
// Returns "enforce" (default for registered), "track", or "off".
//
// Resolution order: anonymous projects always return "off"; otherwise the
// merged Endless config (CLI layer + project layer) supplies an explicit
// "track" or "off"; anything else (including absent config) falls through
// to "enforce".
func GetTrackingMode(projectID int64) string {
	db, err := DB()
	if err != nil {
		return "off"
	}

	var status, projectPath string
	err = db.QueryRow(
		"SELECT status, path FROM projects WHERE id=?", projectID,
	).Scan(&status, &projectPath)
	if err != nil {
		return "off"
	}
	if status == "anonymous" {
		return "off"
	}

	cfg, err := config.Load(dt.DirPath(projectPath))
	if err != nil {
		return "enforce"
	}

	switch cfg.Tracking {
	case "track", "off":
		return cfg.Tracking
	default:
		return "enforce"
	}
}

