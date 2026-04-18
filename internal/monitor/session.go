package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionInfo represents an active AI coding session.
type SessionInfo struct {
	ID           int64
	SessionID    string
	ProjectID    int64
	ActiveGoalID *int64
	State        string
	LastActivity string
}

// StartWorkSession creates or updates an ai_session linked to a specific task.
// Also marks the plan item as in_progress.
func StartWorkSession(sessionID string, projectID int64, taskID int64, workingDir string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	// Upsert ai_sessions
	tmuxPane := os.Getenv("TMUX_PANE")
	_, err = db.Exec(
		`INSERT INTO ai_sessions (session_id, project_id, platform, state, active_goal_id, working_dir, tmux_pane, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   state='working', active_goal_id=?, last_activity=?, project_id=?,
		   tmux_pane=COALESCE(NULLIF(?, ''), tmux_pane)`,
		sessionID, projectID, taskID, workingDir, tmuxPane, now, now,
		taskID, now, projectID, tmuxPane,
	)
	if err != nil {
		return fmt.Errorf("upsert ai_session: %w", err)
	}

	// Mark plan item as in_progress
	_, err = db.Exec(
		"UPDATE plans SET status='in_progress' WHERE id=? AND status IN ('needs_plan','ready','blocked')",
		taskID,
	)
	return err
}

// StartChatSession creates a working session with no task (chat-only).
func StartChatSession(sessionID string, projectID int64, workingDir string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	tmuxPane := os.Getenv("TMUX_PANE")

	_, err = db.Exec(
		`INSERT INTO ai_sessions (session_id, project_id, platform, state, active_goal_id, working_dir, tmux_pane, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'working', NULL, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   state='working', active_goal_id=NULL, last_activity=?,
		   tmux_pane=COALESCE(NULLIF(?, ''), tmux_pane)`,
		sessionID, projectID, workingDir, tmuxPane, now, now,
		now, tmuxPane,
	)
	return err
}

// InitSession creates a session with state='needs_input' on SessionStart.
func InitSession(sessionID string, projectID int64, workingDir string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	_, err = db.Exec(
		`INSERT INTO ai_sessions (session_id, project_id, platform, state, working_dir, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'needs_input', ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET last_activity=?`,
		sessionID, projectID, workingDir, now, now,
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
		`SELECT id, session_id, COALESCE(project_id,0), active_goal_id, state, COALESCE(last_activity,'')
		 FROM ai_sessions WHERE session_id=?`,
		sessionID,
	).Scan(&s.ID, &s.SessionID, &s.ProjectID, &s.ActiveGoalID, &s.State, &s.LastActivity)
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
		"UPDATE ai_sessions SET plan_file_path=? WHERE session_id=?",
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
		"SELECT plan_file_path FROM ai_sessions WHERE session_id=?",
		sessionID,
	).Scan(&path)
	if err != nil || path == nil {
		return ""
	}
	return *path
}

// SetTmuxPane records which tmux pane this session is running in.
func SetTmuxPane(sessionID, pane string) error {
	if pane == "" {
		return nil
	}
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		"UPDATE ai_sessions SET tmux_pane=? WHERE session_id=?",
		pane, sessionID,
	)
	return err
}

// BackfillTmuxPane sets tmux_pane only if it's currently NULL.
func BackfillTmuxPane(sessionID, pane string) error {
	if pane == "" {
		return nil
	}
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		"UPDATE ai_sessions SET tmux_pane=? WHERE session_id=? AND tmux_pane IS NULL",
		pane, sessionID,
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
		"UPDATE ai_sessions SET last_activity=? WHERE session_id=?",
		now, sessionID,
	)
	return err
}

// CompleteTask marks a plan item completed and clears the session's active task.
func CompleteTask(sessionID string, taskID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	// Mark plan item completed
	_, err = db.Exec(
		"UPDATE plans SET status='completed', completed_at=? WHERE id=?",
		now, taskID,
	)
	if err != nil {
		return err
	}

	// Clear active task, set state to idle
	_, err = db.Exec(
		"UPDATE ai_sessions SET active_goal_id=NULL, state='idle', last_activity=? WHERE session_id=?",
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
		"UPDATE ai_sessions SET state='idle', last_activity=? WHERE session_id=?",
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
		"UPDATE ai_sessions SET state='ended', ended_at=?, last_activity=? WHERE session_id=?",
		now, now, sessionID,
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
func GetTrackingMode(projectID int64) string {
	db, err := DB()
	if err != nil {
		return "off"
	}

	// Check if project is anonymous — no enforcement for anonymous projects
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

	// Check .endless/config.json for explicit setting
	configPath := filepath.Join(projectPath, ".endless", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "enforce" // default for registered projects
	}

	var parsed struct {
		Tracking string `json:"tracking"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "enforce"
	}

	switch parsed.Tracking {
	case "track", "off":
		return parsed.Tracking
	default:
		return "enforce"
	}
}

