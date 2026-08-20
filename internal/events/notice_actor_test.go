// Regression tests for E-1917's self-suppression rule.
//
// The shipped implementation stamped tasks.changed_by_session from
// evt.Actor.SessionID unconditionally, on the assumption that a user editing
// from a terminal would produce an empty actor. That assumption was never
// tested against the real resolver and is false: `esu` exports
// ENDLESS_SESSION_ID into the user's own shell, and the sibling-pane inference
// (E-1294) credits a shell pane's command to the adjacent Claude session. Both
// hand a HUMAN's edit the session id of the agent holding the task, so the one
// session that needed the notice was the one systematically silenced.
//
// The suite that shipped alongside that bug seeded changed_by_session directly
// with SQL. It proved the trigger did what it was told and never asked whether
// the value arriving there meant what the design assumed. These tests drive the
// executor with a real environment instead.
package events_test

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

// seedHeldTask creates a task and a live session holding it, so a status change
// through the executor has somebody to notify.
func seedHeldTask(t *testing.T, db *sql.DB, taskID, sessionID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, phase)
		 VALUES (?, 1, 'held task', 'ready', 'now')`, taskID,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
		 VALUES (?, ?, 1, 'claude', 'working', ?, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00')`,
		sessionID, "sess-notice-actor", taskID,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (?, ?, '2026-08-20T00:00:00', '2026-08-20T00:00:00')`,
		sessionID, taskID,
	); err != nil {
		t.Fatalf("seed session_task: %v", err)
	}
}

func noticeCountFor(t *testing.T, db *sql.DB, sessionID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM session_notices WHERE session_id = ?", sessionID,
	).Scan(&n); err != nil {
		t.Fatalf("counting notices: %v", err)
	}
	return n
}

// TestStampTaskActor_HumanEditNotifiesTheHoldingSession is THE regression test.
//
// It reproduces the shipped failure exactly: the event carries the holding
// session's id in Actor.SessionID — which is what the resolver produces for a
// user running `endless task ...` with `esu` active or from a pane beside the
// agent — but CLAUDECODE is absent, so the process is a human's shell. The
// holding session must be told.
//
// Before the fix this asserted 0 notices and the session went on working from a
// status the user had already changed.
func TestStampTaskActor_HumanEditNotifiesTheHoldingSession(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 700, 9100)

	// A human's shell: no CLAUDECODE, but the resolver still credited the
	// adjacent agent session.
	t.Setenv("CLAUDECODE", "")

	evt := newStatusChangedEvent(t, 700, "ready", "revisit")
	evt.Actor.SessionID = "9100"
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := noticeCountFor(t, db, 9100); got != 1 {
		t.Errorf("a human's edit must notify the session holding the task, got %d notices", got)
	}
	var actor sql.NullInt64
	if err := db.QueryRow(
		"SELECT changed_by_session FROM tasks WHERE id = 700").Scan(&actor); err != nil {
		t.Fatalf("reading actor: %v", err)
	}
	if actor.Valid {
		t.Errorf("a non-agent process must stamp a NULL actor, got %d", actor.Int64)
	}
}

// TestStampTaskActor_AgentEditSuppressesItsOwnNotice keeps the original intent
// intact: when the agent itself makes the change, telling it about its own edit
// is noise, and noise is what trains an agent to skim the line that matters.
func TestStampTaskActor_AgentEditSuppressesItsOwnNotice(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 701, 9101)

	// The agent's own tool call: Claude Code exports this into subprocesses.
	t.Setenv("CLAUDECODE", "1")

	evt := newStatusChangedEvent(t, 701, "ready", "revisit")
	evt.Actor.SessionID = "9101"
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := noticeCountFor(t, db, 9101); got != 0 {
		t.Errorf("an agent must not be notified of its own change, got %d notices", got)
	}
	var actor sql.NullInt64
	if err := db.QueryRow(
		"SELECT changed_by_session FROM tasks WHERE id = 701").Scan(&actor); err != nil {
		t.Fatalf("reading actor: %v", err)
	}
	if !actor.Valid || actor.Int64 != 9101 {
		t.Errorf("an agent's change should stamp its session, got %v", actor)
	}
}

// TestStampTaskActor_AgentEditStillNotifiesOtherHolders pins that suppression is
// scoped to the acting session alone. A second session on the same task has no
// idea the first one moved it and must still hear.
func TestStampTaskActor_AgentEditStillNotifiesOtherHolders(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 702, 9102)
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
		 VALUES (9103, 'sess-other', 1, 'claude', 'working', 702, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00')`,
	); err != nil {
		t.Fatalf("seed second session: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (9103, 702, '2026-08-20T00:00:00', '2026-08-20T00:00:00')`,
	); err != nil {
		t.Fatalf("seed second session_task: %v", err)
	}

	t.Setenv("CLAUDECODE", "1")

	evt := newStatusChangedEvent(t, 702, "ready", "revisit")
	evt.Actor.SessionID = "9102"
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := noticeCountFor(t, db, 9102); got != 0 {
		t.Errorf("the acting session should be suppressed, got %d notices", got)
	}
	if got := noticeCountFor(t, db, 9103); got != 1 {
		t.Errorf("the other holder must still be notified, got %d notices", got)
	}
}

// TestStampTaskActor_NoSessionStampsNull covers the plainly-attributable case —
// a background job or a bare invocation with no session at all. It was already
// correct before the fix; it is here so the CLAUDECODE gate cannot regress it.
func TestStampTaskActor_NoSessionStampsNull(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 703, 9104)

	t.Setenv("CLAUDECODE", "1")

	evt := newStatusChangedEvent(t, 703, "ready", "revisit")
	// Actor.SessionID deliberately left empty.
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := noticeCountFor(t, db, 9104); got != 1 {
		t.Errorf("an unattributed change must notify every holder, got %d notices", got)
	}
}
