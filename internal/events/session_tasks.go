package events

import (
	"fmt"
	"strconv"
)

// upsertSessionTask records that the given session touched the given task.
// Called from each task.* executor when the event's actor is a session.
// Inserts a new row or bumps updated_at on conflict.
//
// Runs inside the in-flight tx via the dbQuerier passed by the dispatcher.
// Per E-1315, executors must NOT acquire fresh connections — SQLite has
// SetMaxOpenConns(1) and nested acquires deadlock.
//
// sessionIDStr is Actor.SessionID — the numeric sessions.id encoded as
// text ("356"). Malformed values are skipped without error: the caller's
// guard already restricts this to ActorSession events, but defense in
// depth keeps the upsert from poisoning a task mutation over a stray
// session_id format issue.
func upsertSessionTask(db dbQuerier, sessionIDStr string, taskID int64) error {
	sessionID, parseErr := strconv.ParseInt(sessionIDStr, 10, 64)
	if parseErr != nil {
		return nil
	}
	n := now()
	_, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(session_id, task_id) DO UPDATE SET updated_at = excluded.updated_at`,
		sessionID, taskID, n, n,
	)
	if err != nil {
		return fmt.Errorf("upsert session_tasks (%d, %d): %w", sessionID, taskID, err)
	}
	return nil
}

// shouldRecordSessionTouch reports whether an event should produce a
// session_tasks row. The semantic is "session N touched task M" — what
// matters is whether the event is attributable to a Claude session, not
// the actor channel (cli/hook/web) it came through.
//
// Per event.go's Actor docstring: SessionID is populated whenever the
// event was emitted from within a Claude session's reach, regardless of
// Kind. A `cli` actor with SessionID="42" means "the user ran a CLI
// command from inside Claude session 42" — exactly the touch we want
// to record. The user-facing endless CLI (Python emit_event in
// src/endless/event_bridge.py) defaults actor_kind="cli" but populates
// SessionID via _current_endless_session_id, so the strict
// Kind==ActorSession check used to reject every legitimate touch.
func shouldRecordSessionTouch(evt *Event) bool {
	return evt.Actor.SessionID != ""
}
