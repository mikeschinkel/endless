package monitor

import (
	"database/sql"
	"errors"
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

// StartWorkSession binds the session AND marks the task as underway.
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
		"UPDATE tasks SET status='underway' WHERE id=? AND status IN ('unplanned','ready','blocked')",
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

// RecordBgAgentSession inserts the dispatch-time row for a background agent
// (E-1568). The Python `task spawn --bg` flow calls this (via the
// `session-query record-bg-agent` helper) right after `claude --bg` returns a
// short id but before the bg agent's SessionStart hook fires. The row carries:
//   - session_id NULL    — the real UUID does not exist yet; SessionStart
//                          UPDATEs it later via DecorateBgSession, keyed by
//                          short_id.
//   - kind_id = 2        — background.
//   - active_epic_id     — nearest type='epic' ancestor of taskID (NULL if none).
//   - process NULL       — bg agents have no tmux pane.
//
// project_id and the epic ancestor are resolved here in Go (not Python) to
// avoid a new Python DB read (E-1486). Returns the inserted sessions.id.
func RecordBgAgentSession(taskID int64, shortID string) (int64, error) {
	if shortID == "" {
		return 0, fmt.Errorf("record bg agent session: short_id required")
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}

	// project_id is nullable on both tasks and sessions; a nil pointer inserts
	// NULL rather than 0 (which would be a dangling FK to projects).
	var projectID *int64
	if err = db.QueryRow(
		"SELECT project_id FROM tasks WHERE id=?", taskID,
	).Scan(&projectID); err != nil {
		return 0, fmt.Errorf("resolve project for E-%d: %w", taskID, err)
	}

	epicID, err := nearestEpicAncestor(db, taskID)
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	res, err := db.Exec(
		`INSERT INTO sessions
		   (session_id, project_id, platform, state, active_task_id, active_epic_id, kind_id, short_id, started_at, last_activity)
		 VALUES (NULL, ?, 'claude', 'working', ?, ?, ?, ?, ?, ?)`,
		projectID, taskID, epicID, int64(sessionkind.SessionKindBackground), shortID, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert bg agent session for E-%d: %w", taskID, err)
	}
	return res.LastInsertId()
}

// CountActiveBgAgents returns how many background-agent sessions are currently
// `working` for taskID's project (E-1572). The `task spawn --bg` soft-throttle
// warning calls this before dispatch to tell the coordinator how many bg slots
// the project is already burning; it never blocks. Scope is per project — bg
// agents in unrelated projects do not count toward this project's budget.
//
// project_id is resolved Go-side from the task (mirroring RecordBgAgentSession)
// so the Python flow needs no DB read (E-1486). The just-dispatched agent is not
// yet recorded when this runs, so the count reflects only pre-existing agents.
// The kind filter uses the typed sessionkind constant (not a hardcoded integer),
// keeping it stable against any seed-id change.
func CountActiveBgAgents(taskID int64) (int64, error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}
	// tasks.project_id is NOT NULL (schema), so a plain scan is safe.
	var projectID int64
	if err = db.QueryRow(
		"SELECT project_id FROM tasks WHERE id=?", taskID,
	).Scan(&projectID); err != nil {
		return 0, fmt.Errorf("resolve project for E-%d: %w", taskID, err)
	}
	var n int64
	if err = db.QueryRow(
		`SELECT count(*) FROM sessions
		 WHERE kind_id = ? AND state = 'working' AND project_id = ?`,
		int64(sessionkind.SessionKindBackground), projectID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active bg agents for E-%d: %w", taskID, err)
	}
	return n, nil
}

// nearestEpicAncestor walks up tasks.parent_id from taskID and returns the id
// of the nearest ancestor whose type is 'epic', or nil if none. taskID itself
// is included in the walk (depth 0), so dispatching an epic task directly
// returns its own id.
func nearestEpicAncestor(db *sql.DB, taskID int64) (*int64, error) {
	const q = `
		WITH RECURSIVE ancestry(id, parent_id, type_id, depth) AS (
			SELECT id, parent_id, type_id, 0 FROM tasks WHERE id = ?
			UNION ALL
			SELECT t.id, t.parent_id, t.type_id, a.depth + 1
			FROM tasks t JOIN ancestry a ON t.id = a.parent_id
		)
		SELECT a.id
		FROM ancestry a
		JOIN task_types tt ON tt.id = a.type_id
		WHERE tt.slug = 'epic'
		ORDER BY a.depth
		LIMIT 1`
	var epicID int64
	err := db.QueryRow(q, taskID).Scan(&epicID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve epic ancestor for E-%d: %w", taskID, err)
	}
	return &epicID, nil
}

// DecorateBgSession attaches the real session UUID to a background agent's
// dispatch row once its SessionStart hook fires (E-1568). The row was inserted
// by RecordBgAgentSession with session_id NULL and a short_id; this UPDATEs
// session_id (and bumps last_activity), keyed by short_id and scoped to
// still-undecorated background rows. Returns the number of rows affected — 0
// means no matching dispatch row, so the caller falls through to the normal
// new-row path defensively.
func DecorateBgSession(shortID, sessionID string) (int64, error) {
	if shortID == "" || sessionID == "" {
		return 0, fmt.Errorf("decorate bg session: short_id and session_id required")
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	res, err := db.Exec(
		`UPDATE sessions SET session_id=?, last_activity=?
		 WHERE short_id=? AND kind_id=? AND session_id IS NULL`,
		sessionID, now, shortID, int64(sessionkind.SessionKindBackground),
	)
	if err != nil {
		return 0, fmt.Errorf("decorate bg session %s: %w", shortID, err)
	}
	return res.RowsAffected()
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

