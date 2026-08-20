package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikeschinkel/endless/internal/agentenv"
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
	processID := currentPaneProcessID()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// COALESCE(?, sessions.process_id) keeps the E-1426 rule that a bind with no
	// identifiable pane never stomps a known-good binding. It is now the only
	// protection needed: the reaper that used to destroy the value this defends
	// is gone (E-1898), so there is always something left to preserve.
	_, err = tx.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, process_id, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'working', ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   state='working', active_task_id=?, last_activity=?, project_id=?,
		   process_id=COALESCE(?, sessions.process_id)`,
		sessionID, projectID, taskID, processID, now, now,
		taskID, now, projectID, processID,
	)
	if err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}

	// Active-task-scoped fallback dedup for the empty-pane case (E-1640).
	// TouchSession's collision invalidation keys on `process` (the tmux pane)
	// and is inert when TMUX_PANE is empty, so every fresh-UUID launch (resume,
	// respawn, aborted spawn, /clear) would otherwise leave the prior non-ended
	// row for this task lingering as a duplicate. Now that this session is bound
	// to the task, end any OTHER non-ended foreground row for the same task that
	// has no pane: Endless permits only one live foreground session per task
	// (worktree locks), so any such row is stale. Scoped to kind_id = tmux —
	// background agents (kind_id = background) legitimately carry the task's
	// active_task_id with no pane and are decorated via their own path, so they
	// must never be ended here. Paneless is the only fallback case; rows that
	// hold a real pane are left to TouchSession's pane-collision path.
	dedupWhere := `active_task_id = ?
		   AND session_id != ?
		   AND process_id IS NULL
		   AND kind_id = ?
		   AND state != 'ended'`
	dedupArgs := []any{taskID, sessionID, int64(sessionkind.SessionKindTmux)}

	// Capture the rows about to be ended (within the tx, before the write) so the
	// diagnostic log can name each silently-deduped session. This is exactly the
	// kind of machine-local event a single active_task_id pointer would otherwise
	// erase.
	deduped := collectDedupTargets(tx, dedupWhere, dedupArgs)

	_, err = tx.Exec(`UPDATE sessions SET state = 'ended', last_activity = ? WHERE `+dedupWhere,
		append([]any{now}, dedupArgs...)...)
	if err != nil {
		return fmt.Errorf("dedup stale paneless sessions for task: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	for _, d := range deduped {
		LogSessionTxn(SessionTxn{
			SessionGUID:     d.SessionGUID,
			ShortID:         d.ShortID,
			OldState:        d.State,
			NewState:        "ended",
			OldActiveTaskID: d.ActiveTaskID,
			NewActiveTaskID: d.ActiveTaskID, // dedup ends the row; active_task_id is unchanged
			Reason:          SessionLogDedup,
			Caller:          "monitor.BindSessionToTask",
		})
	}
	return nil
}

// collectDedupTargets reads the sessions the dedup UPDATE is about to end, so
// each can be recorded in the diagnostic log. Best-effort: a read error yields
// no rows (the dedup still proceeds; only the log line is lost).
func collectDedupTargets(tx *sql.Tx, where string, args []any) []SessionSnapshot {
	rows, err := tx.Query(
		`SELECT session_id, short_id, state, active_task_id FROM sessions WHERE `+where,
		args...,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []SessionSnapshot
	for rows.Next() {
		out = append(out, scanSnapshot(rows))
	}
	return out
}

// stampableSession is the value to bind to a changed_by_session sub-select for
// a task mutation this process is about to make: the session id when an AGENT is
// making the change, nil (→ SQL NULL, suppressing nobody) when a person is.
//
// It is the internal/monitor spelling of the rule internal/events applies to
// every task mutation that goes through the event executor — `Actor.Harness !=
// ""` there, agentenv.Present() here, both resolving to the same comparison in
// the same leaf package (E-2006). monitor cannot read an event envelope: events
// imports monitor, so the dependency runs one way only, and these two writes
// bypass the executor entirely.
//
// EXPECTED TO BE A NO-OP FOREVER, and that is the point of adding it. The only
// caller of either write is internal/hookcmd/claude.go, and `hook claude`
// returns before it reads stdin on an unsupported harness (E-1962) — so an
// agent is the only thing that can reach them and an unconditional stamp is
// right BY CONSTRUCTION. Nothing at the call site said so, which is the defect:
// a future caller from the CLI would have inherited a stamp that silences the
// human's own session. This states the dependency instead of leaving it to be
// re-derived. If the gate ever does fire, the caller set changed and the notice
// was already going to the wrong session.
func stampableSession(sessionID string) any {
	if !agentenv.Present() {
		return nil
	}
	return sessionID
}

// StartWorkSession binds the session AND marks the task as underway.
// Defense-in-depth mirror of Python claim_item's emitted events for the
// post-bash `endless task claim` detector — runs in the hook so the next
// hook invocation sees a consistent DB even if the event executor hasn't
// processed task.claimed / task.status_changed yet.
func StartWorkSession(sessionID string, projectID int64, taskID int64) error {
	snap := SnapshotSession(sessionID)
	if err := BindSessionToTask(sessionID, projectID, taskID); err != nil {
		return err
	}
	newTaskID := taskID
	LogSessionTxn(SessionTxn{
		SessionGUID:     sessionID,
		ShortID:         snap.ShortID,
		OldState:        snap.State,
		NewState:        "working", // BindSessionToTask sets state='working'
		OldActiveTaskID: snap.ActiveTaskID,
		NewActiveTaskID: &newTaskID,
		Reason:          SessionLogClaimEvent,
		Caller:          "monitor.StartWorkSession",
	})
	db, err := DB()
	if err != nil {
		return err
	}
	_, err = db.Exec(
		// E-1845: `untriaged` joins the promotable set. Claiming a task IS a
		// person deciding to work on it, which moots the triage question — and
		// without this the claim would bind the session while silently leaving
		// the status at `untriaged`, so the task would read as untouched while
		// someone was actively on it.
		//
		// E-1889: `revisit` joins it for the same reason. Every reopen route
		// now lands `revisit`, so `task spawn --reopen` would otherwise bind a
		// session to a task still reading as not-started. This is the claim
		// path only — the background-session gate (task_cmd's `ready`-only
		// check) is separate and unchanged.
		//
		// changed_by_session (E-1917): this UPDATE does not go through the
		// event executor, so it stamps its own actor. Without it the claim would
		// inherit whichever session last touched the task and notify the wrong
		// people about a status change this session caused. Gated on an agent
		// actually running this process — see stampableSession (E-2006).
		"UPDATE tasks SET status='underway', "+
			"changed_by_session=(SELECT id FROM sessions WHERE session_id=?) "+
			"WHERE id=? AND status IN ('untriaged','unplanned','ready','blocked','revisit')",
		stampableSession(sessionID), taskID,
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
	processID := currentPaneProcessID()

	_, err = db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, process_id, started_at, last_activity)
		 VALUES (?, ?, 'claude', 'working', NULL, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   state='working', active_task_id=NULL, last_activity=?,
		   process_id=COALESCE(?, sessions.process_id)`,
		sessionID, projectID, processID, now, now,
		now, processID,
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
// last_activity, and binds `process_id` when the pane can be given an identity
// (so a pane-reattach is tracked; an unidentifiable pane never stomps a
// previously-known binding). Lifecycle transitions among the LIVE states
// (working/idle/needs_input) are owned by the dedicated helpers
// (BindSessionToTask, IdleSession, EndSession) — TouchSession never clobbers
// a live state on UPDATE, so it can safely fire on every hook event.
//
// `process` is a tmux pane id ("%414") or empty. It is resolved here to a
// processes row paired with the CURRENT tmux server's @server_uuid (E-1898),
// which is what makes a pane id reissued by a later server a different
// identity rather than a collision.
//
// Revival of `ended` (E-1686): the one state transition TouchSession owns.
// An incoming hook is proof the session is alive, so an `ended` row is lifted
// back to 'needs_input' (the same neutral state INSERT uses; the next
// lifecycle hook re-derives working/idle/ended). Without this an `ended` row
// never recovers, and since every reader filters `state != 'ended'` the
// still-live session goes permanently invisible. Gated on the
// ON CONFLICT(session_id) target, NOT a pane match: a reused pane id carries a
// DIFFERENT session_id and takes the INSERT path, so a prior occupant's ended
// row stays ended (E-1530).
//
// This helper no longer performs collision invalidation; see the note at the
// commit below for why that write is gone rather than fixed.
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
	processID := paneProcessID(process)

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// UPSERT: process_id is NULL on INSERT when no pane identity is available,
	// and COALESCEd against the existing value on UPDATE so an unidentifiable
	// bind never overwrites a known-good one. state defaults to 'needs_input'
	// only on INSERT (matches InitSession semantics). On UPDATE state is
	// preserved for every LIVE state and only an 'ended' row is revived to
	// 'needs_input' (E-1686, see the doc comment) — the CASE keeps working↔idle
	// authoritative while giving a stale ending a recovery path.
	_, err = tx.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, started_at, last_activity)
		 VALUES (?, ?, ?, 'needs_input', ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   last_activity = excluded.last_activity,
		   process_id    = COALESCE(excluded.process_id, sessions.process_id),
		   state         = CASE WHEN sessions.state = 'ended' THEN 'needs_input' ELSE sessions.state END`,
		sessionID, projectID, platform, processID, now, now,
	)
	if err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}

	// E-1530's collision invalidation was REMOVED here by E-1898.
	//
	// It used to end every other non-ended row sharing the incoming pane string,
	// on the reasoning that a pane hosts one harness at a time. That reasoning
	// was sound; the KEY was not. A tmux server restart reissues "%414", so the
	// displaced row was frequently a different server's session that was simply
	// unreachable, not dead — E-1468's land run watched a live session get ended
	// this way.
	//
	// With identity as (server_uuid, address) the case cannot arise: a reissued
	// pane resolves to a DIFFERENT processes row, so it never collides with the
	// old binding and there is nothing to invalidate. A genuine same-server
	// collision (two session identities in one pane) leaves both rows alone and
	// readers order by last_activity. No liveness heuristic, no recency window,
	// and — the point — no write that can end a session nobody observed dying.
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
//     UPDATEs it later via DecorateBgSession, keyed by
//     short_id.
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
		"SELECT project_id FROM live_tasks WHERE id=?", taskID,
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
		"SELECT project_id FROM live_tasks WHERE id=?", taskID,
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
			SELECT id, parent_id, type_id, 0 FROM live_tasks WHERE id = ?
			UNION ALL
			SELECT t.id, t.parent_id, t.type_id, a.depth + 1
			FROM live_tasks t JOIN ancestry a ON t.id = a.parent_id
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
		// changed_by_session (E-1917): stamped here for the same reason as
		// StartWorkSession — this path bypasses the event executor's stamp — and
		// gated the same way, see stampableSession (E-2006).
		"UPDATE tasks SET status='confirmed', completed_at=?, "+
			"changed_by_session=(SELECT id FROM sessions WHERE session_id=?) "+
			"WHERE id=?",
		now, stampableSession(sessionID), taskID,
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

	snap := SnapshotSession(sessionID)
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"UPDATE sessions SET state='idle', last_activity=? WHERE session_id=?",
		now, sessionID,
	)
	if err != nil {
		return err
	}
	if snap.Found { // only record a transition that actually had a prior row
		LogSessionTxn(SessionTxn{
			SessionGUID:     sessionID,
			ShortID:         snap.ShortID,
			OldState:        snap.State,
			NewState:        "idle",
			OldActiveTaskID: snap.ActiveTaskID,
			NewActiveTaskID: snap.ActiveTaskID, // idle does not change active_task_id
			Reason:          SessionLogIdle,
			Caller:          "monitor.IdleSession",
		})
	}
	return nil
}

// EndSession marks a session as ended. Also NULLs `process` so reused
// tmux pane ids can't pull the ended row into a lookup after a tmux
// server restart (E-1530, Layer A).
func EndSession(sessionID string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	snap := SnapshotSession(sessionID)
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	// process_id is deliberately PRESERVED across the end (E-1898). Ending a
	// session is a fact about the session, not about where it ran: "session 512
	// ran on pane %414 of server abc-123" stays true, and it is the evidence you
	// need when a binding later looks wrong. E-1530's reason for clearing it —
	// an ended row holding "%414" winning a lookup against a reissued "%414" —
	// no longer applies, because the reissued pane is a different processes row.
	_, err = db.Exec(
		"UPDATE sessions SET state='ended', last_activity=? WHERE session_id=?",
		now, sessionID,
	)
	if err != nil {
		return err
	}
	if snap.Found { // only record a transition that actually had a prior row
		LogSessionTxn(SessionTxn{
			SessionGUID:     sessionID,
			ShortID:         snap.ShortID,
			OldState:        snap.State,
			NewState:        "ended",
			OldActiveTaskID: snap.ActiveTaskID,
			NewActiveTaskID: snap.ActiveTaskID, // end does not change active_task_id
			Reason:          SessionLogEnd,
			Caller:          "monitor.EndSession",
		})
	}
	return nil
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
