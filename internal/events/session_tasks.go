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
// session_tasks row. Strict per E-1322 spec: ActorKind == ActorSession
// only, even though CLI/hook actors may carry a session_id. Don't bind
// cli/hook/system actors as "sessions" in this materialized view.
func shouldRecordSessionTouch(evt *Event) bool {
	return evt.Actor.Kind == ActorSession && evt.Actor.SessionID != ""
}
