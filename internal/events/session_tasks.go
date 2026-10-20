package events

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// upsertSessionTask records that the given session touched the given task.
// Called from each task.* executor when the event's actor is a session.
// Inserts a new row or bumps updated_at on conflict.
//
// relation (E-1462) classifies HOW a task entered this session's scope —
// claimed, bound (`task bind`), touched (`touch`), filed (created/imported
// in-session), revisited (a pre-existing task edited but not claimed), or
// referenced (read-only relevance). On conflict it is UPGRADED, never
// downgraded: an incoming relation that outranks the stored one replaces it,
// anything weaker or equal leaves it alone (sessiontaskrelation.Relation.Rank():
// claimed < bound < touched < filed < revisited < referenced).
//
// E-1696 replaced E-1462's set-once rule with this ladder. Set-once was correct
// only while `referenced` did not exist. The documented happy path is
// `task show <id>` THEN `task claim <id>`, so once a read capture exists,
// set-once would pin every session's own goal task at `referenced` permanently —
// the first touch would win and the claim could never correct it. Set-once
// already had the inverse bug for the same reason: create-then-claim left a
// session's goal reading `filed`.
//
// The write itself is sessiontaskrelation.Record, the one path every
// session_tasks capture takes (E-2291), including the SessionStart auto-bind in
// internal/monitor, which cannot import this package.
//
// Runs inside the in-flight tx via the dbQuerier passed by the dispatcher.
// Per E-1315, executors must NOT acquire fresh connections — SQLite has
// SetMaxOpenConns(1) and nested acquires deadlock.
//
// sessionIDStr is Actor.SessionID — the numeric sessions.id encoded as
// text ("356"). Malformed values are skipped without error: the caller's
// guard already restricts this to events where Actor.SessionID is
// non-empty, but defense in depth keeps the upsert from poisoning a
// task mutation if a non-numeric SessionID ever slips through an
// upstream emitter.
func upsertSessionTask(db dbQuerier, sessionIDStr string, taskID int64, relation sessiontaskrelation.Relation) error {
	sessionID, parseErr := strconv.ParseInt(sessionIDStr, 10, 64)
	if parseErr != nil {
		return nil
	}
	return sessiontaskrelation.Record(db, sessionID, taskID, relation, now())
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

// parseTaskDisplayID parses a task display id ("E-100", case-insensitive
// prefix, or a bare number) into its numeric tasks.id. Used by the two
// session-task membership executors (session_task_membership.go).
func parseTaskDisplayID(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "E-")
	s = strings.TrimPrefix(s, "e-")
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, refusal.NoReport(
			fmt.Sprintf("events: malformed task id %q (expected E-NNN)", raw),
			"Use the E-NNN form and retry",
		).Cause(err)
	}
	return id, nil
}
