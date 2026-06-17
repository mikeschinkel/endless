package monitor

import (
	"fmt"
	"os"
	"time"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/sessionkind"
	"github.com/mikeschinkel/go-dt"
)

// SessionInfo represents an active AI coding session.
//
// ActiveEpicID is the epic task id when the session works under an epic (and
// ActiveTaskID tracks the viewed child); nil otherwise. Kind discriminates a
// pane-bound 'tmux' session from a headless 'background' agent (E-1571).
type SessionInfo struct {
	ID           int64
	SessionID    string
	ProjectID    int64
	ActiveTaskID *int64
	ActiveEpicID *int64
	Kind         sessionkind.SessionKind
	State        string
	LastActivity string
	StartedAt    string
}

// BindSessionToTask creates or updates a session row and points its
// active_task_id at taskID. Does NOT change task status — the caller
// (typically the Python claim_item via emitted events, or the spawn
// pre-claim flow) owns that. Used by SessionStart's spawn-marker
// auto-bind (claude.go) where the status was already flipped by spawn
// before Claude launched.
func BindSessionToTask(sessionID string, projectID int64, taskID int64) error {
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
	return nil
}

// StartWorkSession binds the session AND marks the task as in_progress.
// Defense-in-depth mirror of Python claim_item's emitted events for the
// post-bash `endless task claim` detector — runs in the hook so the next
// hook invocation sees a consistent DB even if the event executor hasn't
// processed task.claimed / task.status_changed yet.
func StartWorkSession(sessionID string, projectID int64, taskID int64) error {
	if err := BindSessionToTask(sessionID, projectID, taskID); err != nil {
		return err
	}
	db, err := DB()
	if err != nil {
		return err
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
	var kindID int64
	err = db.QueryRow(
		`SELECT id, session_id, COALESCE(project_id,0), active_task_id, active_epic_id, kind_id, state, COALESCE(last_activity,''), COALESCE(started_at,'')
		 FROM sessions WHERE session_id=?`,
		sessionID,
	).Scan(&s.ID, &s.SessionID, &s.ProjectID, &s.ActiveTaskID, &s.ActiveEpicID, &kindID, &s.State, &s.LastActivity, &s.StartedAt)
	if err != nil {
		return nil, err
	}
	s.Kind = sessionkind.SessionKind(kindID)
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

// TouchSession is the per-event UPSERT helper. It records the session's
// presence in the sessions table (creating the row if absent), refreshes
// last_activity, and overwrites `process` when the new value is non-empty
// (so a pane-reattach is tracked; an empty TMUX_PANE never stomps a
// previously-known value). Lifecycle transitions (working/idle/ended) are
// owned by the dedicated helpers (BindSessionToTask, IdleSession,
// EndSession) — TouchSession deliberately does not change `state` on
// UPDATE, so it can safely fire on every hook event.
//
// Collision invalidation: when `process` is non-empty and matches a row
// other than this session, that other row is marked ended in the same
// transaction. A pane can only host one harness at a time, so the prior
// occupant must be dead.
//
// The platform parameter lets future non-Claude harnesses share this
// helper; today the only caller passes "claude".
func TouchSession(sessionID, platform, process string, projectID int64) error {
	if sessionID == "" {
		return fmt.Errorf("touch session: session_id required")
	}
	if platform == "" {
		return fmt.Errorf("touch session: platform required")
	}
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// UPSERT: process is NULL on INSERT when the new value is empty, and
	// COALESCEd against the existing value on UPDATE so an empty input
	// never overwrites a known-good process. state defaults to
	// 'needs_input' only on INSERT (matches InitSession semantics); UPDATE
	// never touches state.
	_, err = tx.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process, started_at, last_activity)
		 VALUES (?, ?, ?, 'needs_input', NULLIF(?, ''), ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   last_activity = excluded.last_activity,
		   process       = COALESCE(NULLIF(excluded.process, ''), sessions.process)`,
		sessionID, projectID, platform, process, now, now,
	)
	if err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}

	// Collision invalidation: only meaningful when the incoming process
	// is non-empty (otherwise we can't be claiming any pane). NULLs the
	// displaced row's `process` so reused pane ids after a tmux server
	// restart can't pull it back into a lookup (E-1530, Layer A).
	// E-1468 plans to revisit this site's logic (the displaced row may
	// not actually be dead — a tmux server restart can reissue the same
	// pane id to a different session); the NULL is independent of that.
	if process != "" {
		_, err = tx.Exec(
			`UPDATE sessions
			 SET state = 'ended', process = NULL, last_activity = ?
			 WHERE process = ?
			   AND session_id != ?
			   AND state != 'ended'`,
			now, process, sessionID,
		)
		if err != nil {
			return fmt.Errorf("collision invalidation: %w", err)
		}
	}

	return tx.Commit()
}

// EnsureClaudeSessionID looks up (or lazy-creates) the integer sessions.id
// for an env-identified Claude session and returns it. Used by the Python
// resolver's env-vars-as-truth layer (E-1455): when CLAUDECODE=1 and
// CLAUDE_CODE_SESSION_ID are set, the current pane unambiguously identifies
// itself as a Claude session — but the DB row may not exist yet if no hook
// event has fired in this session. This helper composes TouchSession (which
// idempotently INSERT-or-UPSERTs the session row, with collision
// invalidation on `process`) with a follow-up id lookup.
//
// process is the TMUX_PANE value (may be empty when running outside tmux).
// projectID is the resolved current-cwd project id; pass 0 if unresolved.
func EnsureClaudeSessionID(sessionID, process string, projectID int64) (int64, error) {
	if sessionID == "" {
		return 0, fmt.Errorf("ensure claude session id: session_id required")
	}
	if err := TouchSession(sessionID, "claude", process, projectID); err != nil {
		return 0, err
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}
	var id int64
	if err := db.QueryRow(
		"SELECT id FROM sessions WHERE session_id = ?", sessionID,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("lookup sessions.id for %s: %w", sessionID, err)
	}
	return id, nil
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

// EndSession marks a session as ended. Also NULLs `process` so reused
// tmux pane ids can't pull the ended row into a lookup after a tmux
// server restart (E-1530, Layer A).
func EndSession(sessionID string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"UPDATE sessions SET state='ended', process=NULL, last_activity=? WHERE session_id=?",
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

