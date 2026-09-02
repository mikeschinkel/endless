package monitor

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/mikeschinkel/endless/internal/agentenv"
	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/go-dt"
)

// liveSessionStates is sessionstate.Live rendered for a SQL IN clause, the
// session counterpart to terminalStatusSet in session_status.go. Rendered once
// at init because every reader in this package asks the same question — "is this
// session still able to act?" — and it was twenty-nine hand-written
// `state != 'ended'` clauses before E-2105.
//
// A membership list rather than the negation it replaced, deliberately: a state
// added later has to be classified into Live or left out of it, instead of
// inheriting "live" from having simply not been `ended`.
var liveSessionStates = sessionstate.SQLList(sessionstate.Live)

// SessionInfo represents an active AI coding session.
//
// EpicID is the epic task id when the session works under an epic (and
// TaskID tracks the viewed child); nil otherwise.
type SessionInfo struct {
	ID           int64
	SessionID    string
	ProjectID    int64
	TaskID       *int64
	EpicID       *int64
	State        string
	LastActivity string
	StartedAt    string
}

// BindSessionToTask creates or updates a session row and points its
// task_id at taskID. Does NOT change task status — the caller
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
		`INSERT INTO sessions (session_id, project_id, platform, state, task_id, process_id, started_at, last_activity)
		 VALUES (?, ?, 'claude', ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   state=?, task_id=?, last_activity=?, project_id=?,
		   process_id=COALESCE(?, sessions.process_id)`,
		sessionID, projectID, sessionstate.Working, taskID, processID, now, now,
		sessionstate.Working, taskID, now, projectID, processID,
	)
	if err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}

	// Active-task-scoped fallback dedup for the empty-pane case (E-1640).
	// TouchSession's collision invalidation keys on `process` (the tmux pane)
	// and is inert when TMUX_PANE is empty, so every fresh-UUID launch (resume,
	// respawn, aborted spawn, /clear) would otherwise leave the prior non-ended
	// row for this task lingering as a duplicate. Now that this session is bound
	// to the task, end any OTHER non-ended row for the same task that has no
	// pane: Endless permits only one live session per task (worktree locks), so
	// any such row is stale. Paneless is the only fallback case; rows that hold
	// a real pane are left to TouchSession's pane-collision path.
	//
	// E-2074 dropped the `AND kind_id = tmux` scope this carried. It existed to
	// spare background-agent rows, which legitimately held a task_id with no
	// pane and were decorated by their own path. Background agents are gone, so
	// every paneless row reaching here is the stale foreground row the sweep was
	// always meant to end.
	dedupWhere := `task_id = ?
		   AND session_id != ?
		   AND process_id IS NULL
		   AND state IN (` + liveSessionStates + `)`
	dedupArgs := []any{taskID, sessionID}

	// Capture the rows about to be ended (within the tx, before the write) so the
	// diagnostic log can name each silently-deduped session. This is exactly the
	// kind of machine-local event a single task_id pointer would otherwise
	// erase.
	deduped := collectDedupTargets(tx, dedupWhere, dedupArgs)

	_, err = tx.Exec(`UPDATE sessions SET state = ?, last_activity = ? WHERE `+dedupWhere,
		append([]any{sessionstate.Ended, now}, dedupArgs...)...)
	if err != nil {
		return fmt.Errorf("dedup stale paneless sessions for task: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	for _, d := range deduped {
		LogSessionTxn(SessionTxn{
			SessionGUID: d.SessionGUID,
			OldState:    d.State,
			NewState:    sessionstate.Ended,
			OldTaskID:   d.TaskID,
			NewTaskID:   d.TaskID, // dedup ends the row; task_id is unchanged
			Reason:      SessionLogDedup,
			Caller:      "monitor.BindSessionToTask",
		})
	}
	return nil
}

// collectDedupTargets reads the sessions the dedup UPDATE is about to end, so
// each can be recorded in the diagnostic log. Best-effort: a read error yields
// no rows (the dedup still proceeds; only the log line is lost).
func collectDedupTargets(tx *sql.Tx, where string, args []any) []SessionSnapshot {
	rows, err := tx.Query(
		`SELECT session_id, state, task_id FROM sessions WHERE `+where,
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
		SessionGUID: sessionID,
		OldState:    snap.State,
		NewState:    sessionstate.Working, // what BindSessionToTask just wrote
		OldTaskID:   snap.TaskID,
		NewTaskID:   &newTaskID,
		Reason:      SessionLogClaimEvent,
		Caller:      "monitor.StartWorkSession",
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
		// now lands `revisit`, so a reopened task would otherwise bind a
		// session while still reading as not-started. This is the claim
		// path only — the background-session gate (task_cmd's `ready`-only
		// check) is separate and unchanged. (E-1968 retired the route that
		// motivated this, `task spawn --reopen`; the promotion still matters
		// for `task claim` on a task reopened any other way.)
		//
		// changed_by_session (E-1917): this UPDATE does not go through the
		// event executor, so it stamps its own actor. Without it the claim would
		// inherit whichever session last touched the task and notify the wrong
		// people about a status change this session caused. Gated on an agent
		// actually running this process — see stampableSession (E-2006).
		"UPDATE tasks SET status='underway', "+
			"changed_by_session=(SELECT id FROM sessions WHERE session_id=?) "+
			"WHERE id=? AND status IN ("+taskstatus.SQLList(taskstatus.ClaimPromotes)+")",
		stampableSession(sessionID), taskID,
	)
	return err
}

// StartChatSession creates a working session with no task (chat-only).
//
// E-1968 / ED-1560: the ON CONFLICT branch no longer sets task_id=NULL.
// `task chat` on an already-known session id used to unbind whatever task that
// session held, which is the write-once column being cleared by a verb that has
// nothing to say about task ownership. A session that already holds a task is
// not chat-only; the INSERT branch still binds NULL for a genuinely new one.
func StartChatSession(sessionID string, projectID int64) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	processID := currentPaneProcessID()

	_, err = db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, task_id, process_id, started_at, last_activity)
		 VALUES (?, ?, 'claude', ?, NULL, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   state=?, last_activity=?,
		   process_id=COALESCE(?, sessions.process_id)`,
		sessionID, projectID, sessionstate.Working, processID, now, now,
		sessionstate.Working, now, processID,
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
		 VALUES (?, ?, 'claude', ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET last_activity=?`,
		sessionID, projectID, sessionstate.NeedsInput, now, now,
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
		`SELECT id, session_id, COALESCE(project_id,0), task_id, epic_id, state, COALESCE(last_activity,''), COALESCE(started_at,'')
		 FROM sessions WHERE session_id=?`,
		sessionID,
	).Scan(&s.ID, &s.SessionID, &s.ProjectID, &s.TaskID, &s.EpicID, &s.State, &s.LastActivity, &s.StartedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
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
// never recovers, and since every reader filters on sessionstate.Live the
// still-live session goes permanently invisible. Gated on the
// ON CONFLICT(session_id) target, NOT a pane match: a reused pane id carries a
// DIFFERENT session_id and takes the INSERT path, so a prior occupant's ended
// row stays ended (E-1530).
//
// It does NOT wake an idle session; WakeSession does, and the split is
// deliberate (E-2093). TouchSession is reached by callers that are NOT the
// session — EnsureClaudeSessionID resolves a Claude session's row on behalf of
// a SIBLING SHELL pane reading the window's UUID, and that caller has observed
// nothing about whether the session is acting. A wake in here would fire on
// every `endless` command a human ran in the shell pane (and on every repaint
// of the monitor pane beside it), so an idle session would read as `working`
// forever. See WakeSession for where the rule lives instead.
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
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   last_activity = excluded.last_activity,
		   process_id    = COALESCE(excluded.process_id, sessions.process_id),
		   state         = CASE WHEN sessions.state = ? THEN ? ELSE sessions.state END`,
		sessionID, projectID, platform, sessionstate.NeedsInput, processID, now, now,
		sessionstate.Ended, sessionstate.NeedsInput,
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

// WakeSession is the missing half of `Stop` (E-2093).
//
// `Stop` marks a session `idle` at the end of every turn. Nothing marked it
// `working` again, so a session that completed one clean turn stayed `idle` for
// the rest of its life — and the PreToolUse gate, which admitted `working`
// alone, then refused every write it attempted. Two sessions were stranded that
// way, and neither had a command that could recover: `task claim` refuses on
// status first, and `--force` cleared that only by demoting the task.
//
// The rule it implements is "a session holding a task, observed acting, is
// working", stated that way so a future entry point inherits it:
//
//   - OBSERVED ACTING is the caller's assertion, which is why this is a
//     separate verb rather than a clause inside TouchSession. Only a hook event
//     is evidence that THIS session did something; TouchSession is also reached
//     on a session's behalf by a sibling shell pane, which has observed nothing.
//   - HOLDING A TASK is the precondition, checked in the same statement that
//     writes. It restores a declaration already made and must never manufacture
//     one, so a session that never claimed is left exactly as it was — that is
//     the case the gate exists to refuse.
//
// `needs_input` is deliberately not woken: it means a human was asked something
// and has not answered, and only the human answering ends it. Waking it would
// erase the one state that says a session is waiting on a person. `ended` is
// not woken either — an incoming event revives it to `needs_input` in
// TouchSession, and reviving a dead row into work is `bind`'s job.
//
// Caller placement matters as much as the rule. It belongs at the single
// per-event point every turn reaches, NOT in the UserPromptSubmit handler: a
// turn begun from a `!` bash-input fires no UserPromptSubmit, so waking there
// alone would reproduce the same bug in a narrower and harder-to-see form.
// `hook claude` calls it beside TouchSession, before any event-specific
// branching, so every event of every turn goes through it.
//
// Idempotent and self-conditioning: the WHERE clause is the whole rule, so a
// second call is a no-op and there is no read-then-write window. RowsAffected
// is what says the transition happened, which is also what gates the log —
// `sessions` is not journaled, so without that line the idle→working edge would
// have no history at all.
func WakeSession(sessionID string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	snap := SnapshotSession(sessionID)
	res, err := db.Exec(
		`UPDATE sessions SET state = ?
		  WHERE session_id = ? AND state = ? AND task_id IS NOT NULL`,
		sessionstate.Working, sessionID, sessionstate.Idle,
	)
	if err != nil {
		return fmt.Errorf("wake session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return nil
	}
	LogSessionTxn(SessionTxn{
		SessionGUID: sessionID,
		OldState:    snap.State,
		NewState:    sessionstate.Working,
		OldTaskID:   snap.TaskID,
		NewTaskID:   snap.TaskID, // the wake does not change task_id
		Reason:      SessionLogWake,
		Caller:      "monitor.WakeSession",
	})
	return nil
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

// CompleteTask marks a task as confirmed and idles the session.
//
// E-1968 / ED-1560: it no longer clears task_id. The session that
// confirmed the task is still the session that WORKED it, and that pointer is
// the only way back to its transcript — `session goto E-<id> --resume` resolves
// through it. Clearing it on completion made a finished task's session
// unfindable by task ref, and reported it as one that never claimed a task.
// The column is write-once: set at claim, never cleared, never repointed.
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

	// Idle the session; the task binding stays (see the doc comment).
	_, err = db.Exec(
		"UPDATE sessions SET state=?, last_activity=? WHERE session_id=?",
		sessionstate.Idle, now, sessionID,
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
		"UPDATE sessions SET state=?, last_activity=? WHERE session_id=?",
		sessionstate.Idle, now, sessionID,
	)
	if err != nil {
		return err
	}
	if snap.Found { // only record a transition that actually had a prior row
		LogSessionTxn(SessionTxn{
			SessionGUID: sessionID,
			OldState:    snap.State,
			NewState:    sessionstate.Idle,
			OldTaskID:   snap.TaskID,
			NewTaskID:   snap.TaskID, // idle does not change task_id
			Reason:      SessionLogIdle,
			Caller:      "monitor.IdleSession",
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
		"UPDATE sessions SET state=?, last_activity=? WHERE session_id=?",
		sessionstate.Ended, now, sessionID,
	)
	if err != nil {
		return err
	}
	if snap.Found { // only record a transition that actually had a prior row
		LogSessionTxn(SessionTxn{
			SessionGUID: sessionID,
			OldState:    snap.State,
			NewState:    sessionstate.Ended,
			OldTaskID:   snap.TaskID,
			NewTaskID:   snap.TaskID, // end does not change task_id
			Reason:      SessionLogEnd,
			Caller:      "monitor.EndSession",
		})
	}
	return nil
}

// IsSessionExpired was deleted by E-2093 along with its only caller.
//
// The PreToolUse gate used to refuse a `working` session whose `last_activity`
// was over 30 minutes old, with "your work session has expired due to
// inactivity". That branch had been unreachable since E-1426 put a per-event
// TouchSession at the top of every hook invocation: by the time the gate reads
// the row, `last_activity` is always the current instant. It could not fire,
// and it named `endless task claim <id>` — a command that refuses on status
// before it reaches the question the refusal was about.
//
// Nothing replaces it. Liveness is answered by observing the pane
// (internal/monitor/liveness.go), not by a timestamp the hook itself keeps
// resetting.

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

	// The column is STORED form; config.Load reads a directory (E-2011).
	projectPath, err = ResolvedProjectPath(projectPath)
	if err != nil {
		return "enforce"
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
