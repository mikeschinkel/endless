package monitor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The machine-local diagnostic log (user-machine.jsonl).
//
// This is an append-only, machine-local record of user-machine state
// transitions — session bindings first; later worktree locks, sandbox
// routing, hook skips. It is purely observational: it is NOT the shareable
// ledger and NOT a write-ahead log. Nothing here is ever replayed into the
// DB or shared with other developers. It exists so that an incident in
// machine-local state (a session's task_id being silently rebound,
// say) leaves a trail to inspect after the fact — sessions are intentionally
// not journaled, so without this log a single-pointer field like
// task_id has no history.
//
// Every write is best-effort: a logging failure must NEVER block a hook or a
// session write, so all errors here are swallowed.

// userMachineLogPath is the single resolver for the diagnostic log's location.
// It is XDG_CONFIG_HOME-routed via ConfigDir(), so a worktree sandbox gets its
// own file rather than polluting the real one. The location may move later;
// keep every reference behind this one function so a future move is one change.
func userMachineLogPath() string {
	return filepath.Join(ConfigDir(), "log", "user-machine.jsonl")
}

// SessionLogReason names why a session-state transition happened. It is the
// discriminator the diagnostic log carries for a "session" line; a future
// investigator greps on it to reconstruct what drove a change.
type SessionLogReason string

const (
	SessionLogCwdBind    SessionLogReason = "cwd-bind"    // SessionStart cwd-derived auto-bind
	SessionLogSpawnBind  SessionLogReason = "spawn-bind"  // SessionStart spawn-marker auto-bind
	SessionLogClaimEvent SessionLogReason = "claim-event" // task.claimed executor / claim hook mirror
	SessionLogIdle       SessionLogReason = "idle"        // IdleSession
	SessionLogEnd        SessionLogReason = "end"         // EndSession
	SessionLogDedup      SessionLogReason = "dedup"       // paneless stale-row dedup
	SessionLogRelease    SessionLogReason = "release"     // task.released executor
)

// sessionLogEntry is one line in the diagnostic log. The top-level `kind`
// discriminator ("session" here) lets the file carry more than one
// machine-local concern over time without changing readers. Struct (not a
// map) so field order and shape are stable.
type sessionLogEntry struct {
	Kind      string `json:"kind"` // always "session" for now
	TS        string `json:"ts"`
	SessionID string `json:"session_id,omitempty"` // session GUID
	OldState  string `json:"old_state,omitempty"`
	NewState  string `json:"new_state,omitempty"`
	OldTaskID *int64 `json:"old_task_id,omitempty"`
	NewTaskID *int64 `json:"new_task_id,omitempty"`
	Reason    string `json:"reason"`
	Cwd       string `json:"cwd,omitempty"`
	TmuxPane  string `json:"tmux_pane,omitempty"`
	Caller    string `json:"caller,omitempty"`
}

// SessionTxn is one session-state transition to record. The caller supplies the
// old side (captured via SnapshotSession / SnapshotSessionByID before the write)
// and the new side; LogSessionTxn fills in ts, cwd, and tmux_pane from the
// current process.
type SessionTxn struct {
	SessionGUID string
	OldState    string
	NewState    string
	OldTaskID   *int64
	NewTaskID   *int64
	Reason      SessionLogReason
	Caller      string
}

// SessionSnapshot is a session's pre-write state, captured for the log's "old"
// side. Found is false when no row matched (e.g. a first-time bind that INSERTs);
// its zero fields then correctly represent "no prior state".
type SessionSnapshot struct {
	SessionGUID string
	State       string
	TaskID      *int64
	Found       bool
}

// RowQuerier is the read surface shared by *sql.DB and *sql.Tx. The events
// executor passes its transaction so a snapshot reflects the same view the
// write will mutate.
type RowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// SnapshotSession reads a session's current state by GUID for the diagnostic
// log's "old" side. Best-effort: a missing row or any read error yields a
// zero-value SessionSnapshot (Found=false). Call BEFORE the write.
func SnapshotSession(sessionID string) SessionSnapshot {
	db, err := DB()
	if err != nil {
		return SessionSnapshot{}
	}
	return scanSnapshot(
		db.QueryRow(
			"SELECT session_id, state, task_id FROM sessions WHERE session_id=?",
			sessionID,
		),
	)
}

// SnapshotSessionByID reads a session's current state by numeric row id, using
// the caller's querier (a *sql.Tx during event processing) so the snapshot sees
// the same rows the pending write will change. Best-effort; see SnapshotSession.
func SnapshotSessionByID(q RowQuerier, id int64) SessionSnapshot {
	return scanSnapshot(
		q.QueryRow(
			"SELECT session_id, state, task_id FROM sessions WHERE id=?",
			id,
		),
	)
}

// scanner is satisfied by both *sql.Row (QueryRow) and *sql.Rows (Query), so
// scanSnapshot serves the single-row snapshot and the multi-row dedup sweep.
type scanner interface {
	Scan(dest ...any) error
}

func scanSnapshot(row scanner) SessionSnapshot {
	var guid, state sql.NullString
	var taskID sql.NullInt64
	if err := row.Scan(&guid, &state, &taskID); err != nil {
		return SessionSnapshot{}
	}
	snap := SessionSnapshot{
		SessionGUID: guid.String,
		State:       state.String,
		Found:       true,
	}
	if taskID.Valid {
		v := taskID.Int64
		snap.TaskID = &v
	}
	return snap
}

// LogSessionTxn appends one "session" line to the diagnostic log. Best-effort:
// any failure (marshal, mkdir, open, write) is swallowed so logging can never
// block the session write it is documenting.
func LogSessionTxn(t SessionTxn) {
	appendUserMachineLog(sessionLogEntry{
		Kind:      "session",
		TS:        time.Now().UTC().Format(time.RFC3339),
		SessionID: t.SessionGUID,
		OldState:  t.OldState,
		NewState:  t.NewState,
		OldTaskID: t.OldTaskID,
		NewTaskID: t.NewTaskID,
		Reason:    string(t.Reason),
		Cwd:       currentCwd(),
		TmuxPane:  os.Getenv("TMUX_PANE"),
		Caller:    t.Caller,
	})
}

func currentCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// appendUserMachineLog marshals one entry and appends it as a single line. The
// whole line (including the newline) is written in one Write call so concurrent
// hook processes appending to the same file interleave atomically (a JSON line
// is far below PIPE_BUF, so O_APPEND writes don't tear).
func appendUserMachineLog(entry sessionLogEntry) {
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	dir := filepath.Dir(userMachineLogPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(userMachineLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
