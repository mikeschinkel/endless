package events

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// upsertSessionTask records that the given session touched the given task.
// Called from each task.* executor when the event's actor is a session.
// Inserts a new row or bumps updated_at on conflict.
//
// relation (E-1462) classifies HOW the task entered this session's scope —
// goal (claimed), surfaced (created/imported in-session), or revisited (a
// pre-existing task touched but not claimed). It is set ONCE, on insert, and
// deliberately left unchanged on conflict: the first event to bring a task into
// the session decides its relation, and a later incidental touch must not
// downgrade a goal/surfaced task to revisited (set-once semantics). NULL is
// reserved for pre-E-1462 historical rows; new captures always pass a relation.
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
	n := now()
	_, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, task_id) DO UPDATE SET updated_at = excluded.updated_at`,
		sessionID, taskID, int(relation), n, n,
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

// execSessionTasksOrdered handles the KindSessionTasksOrdered event (E-1683):
// it sets the per-session implementation order (session_tasks.do_order) for the
// emitting session's touched tasks. Replace-all — the payload's groups are the
// complete ordering, so every other do_order for this session is reset to NULL.
//
// Resolution mirrors execSessionStatusRecorded: the session id arrives in the
// payload's `process` field, either as the "__session_id=N" sentinel (validated
// directly) or a raw tmux pane id (looked up in-transaction). Every task id in
// the spec must already be a session_tasks row for this session; an unknown or
// foreign id fails the whole operation (the open transaction rolls back).
//
// do_order numbering: group index i (0-based) → do_order = i+1. Ids within the
// same group share a do_order (parallelizable). updated_at is deliberately not
// bumped — reordering the plan is not task work, and the reap-worktrees
// staleness clock keys on session_tasks.updated_at.
func execSessionTasksOrdered(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p SessionTasksOrderedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal session_tasks.ordered payload: %w", err)
	}

	sessionID, ok, err := sessionIDFromSentinel(db, p.Process)
	if err != nil {
		return nil, err
	}
	if !ok {
		sessionID, err = liveSessionByProcessTx(db, p.Process)
		if err != nil {
			return nil, fmt.Errorf(
				"events: no live session for process %q: %w", p.Process, err,
			)
		}
	}

	// Parse "E-100" → 100 for every id, preserving group structure, and reject
	// a task id that appears in more than one group (an ambiguous order).
	orderByTaskID := make(map[int64]int)
	for i, group := range p.Groups {
		if len(group) == 0 {
			return nil, fmt.Errorf("events: session_tasks.ordered group %d is empty", i+1)
		}
		for _, raw := range group {
			taskID, perr := parseTaskDisplayID(raw)
			if perr != nil {
				return nil, perr
			}
			if _, dup := orderByTaskID[taskID]; dup {
				return nil, fmt.Errorf(
					"events: session_tasks.ordered lists task E-%d more than once", taskID,
				)
			}
			orderByTaskID[taskID] = i + 1
		}
	}

	// Validate membership: every spec id must already be a session_tasks row for
	// this session. Reject unknown/foreign ids before mutating anything.
	member, err := sessionTaskIDs(db, sessionID)
	if err != nil {
		return nil, fmt.Errorf("events: load session_tasks for session %d: %w", sessionID, err)
	}
	var foreign []string
	for taskID := range orderByTaskID {
		if !member[taskID] {
			foreign = append(foreign, fmt.Sprintf("E-%d", taskID))
		}
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return nil, fmt.Errorf(
			"events: session_tasks.ordered references task(s) not in this session: %s",
			strings.Join(foreign, ", "),
		)
	}

	// Replace-all: clear any prior ordering for this session, then set the new.
	if _, err := db.Exec(
		`UPDATE session_tasks SET do_order = NULL
		 WHERE session_id = ? AND do_order IS NOT NULL`,
		sessionID,
	); err != nil {
		return nil, fmt.Errorf("events: clear session_tasks order: %w", err)
	}
	for taskID, ord := range orderByTaskID {
		if _, err := db.Exec(
			`UPDATE session_tasks SET do_order = ?
			 WHERE session_id = ? AND task_id = ?`,
			ord, sessionID, taskID,
		); err != nil {
			return nil, fmt.Errorf("events: set session_tasks order for E-%d: %w", taskID, err)
		}
	}

	return &ExecuteResult{Markdown: renderSessionTasksOrder(p.Groups)}, nil
}

// parseTaskDisplayID parses a task display id ("E-100", case-insensitive
// prefix, or a bare number) into its numeric tasks.id.
func parseTaskDisplayID(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "E-")
	s = strings.TrimPrefix(s, "e-")
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("events: malformed task id %q (expected E-NNN)", raw)
	}
	return id, nil
}

// sessionTaskIDs returns the set of task ids the session has touched.
func sessionTaskIDs(db dbQuerier, sessionID int64) (map[int64]bool, error) {
	rows, err := db.Query(
		`SELECT task_id FROM session_tasks WHERE session_id = ?`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	return set, rows.Err()
}

// renderSessionTasksOrder formats the applied ordering as markdown for chat,
// one line per order with parallel groups joined by " ∥ ".
func renderSessionTasksOrder(groups [][]string) string {
	var b strings.Builder
	b.WriteString("Set implementation order:\n\n")
	for i, group := range groups {
		canon := make([]string, len(group))
		for j, raw := range group {
			if id, err := parseTaskDisplayID(raw); err == nil {
				canon[j] = fmt.Sprintf("E-%d", id)
			} else {
				canon[j] = raw
			}
		}
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.Join(canon, " ∥ "))
	}
	return b.String()
}
