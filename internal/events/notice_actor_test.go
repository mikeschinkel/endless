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
// E-2006 changed only HOW these tests declare agent-ness, not what they assert.
// The executor read os.Getenv("CLAUDECODE") when this suite was written; it
// reads evt.Actor.Harness now — the same fact, recorded on the envelope by
// events.EmittingActor instead of re-sniffed from the process. So the setup line
// moved from t.Setenv onto the event, and every assertion and failure message
// below is byte-for-byte what E-1917 shipped.
//
// The suite that shipped alongside that bug seeded changed_by_session directly
// with SQL. It proved the trigger did what it was told and never asked whether
// the value arriving there meant what the design assumed. These tests drive the
// executor with a real event envelope instead.
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
		`INSERT INTO sessions (id, session_id, project_id, platform, state, task_id, kind_id, started_at, last_activity)
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
// agent — but the envelope carries no harness, so a person made this change.
// The holding session must be told.
//
// Before the fix this asserted 0 notices and the session went on working from a
// status the user had already changed.
func TestStampTaskActor_HumanEditNotifiesTheHoldingSession(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 700, 9100)

	evt := newStatusChangedEvent(t, 700, "ready", "revisit")
	// A human's shell: no harness on the envelope, but the resolver still
	// credited the adjacent agent session.
	evt.Actor.Harness = ""
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

	evt := newStatusChangedEvent(t, 701, "ready", "revisit")
	// The agent's own tool call: EmittingActor observed a harness and recorded
	// it on the envelope.
	evt.Actor.Harness = "claude_cli"
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
		`INSERT INTO sessions (id, session_id, project_id, platform, state, task_id, kind_id, started_at, last_activity)
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

	evt := newStatusChangedEvent(t, 702, "ready", "revisit")
	evt.Actor.Harness = "claude_cli"
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
// correct before the fix; it is here so the harness gate cannot regress it.
func TestStampTaskActor_NoSessionStampsNull(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 703, 9104)

	evt := newStatusChangedEvent(t, 703, "ready", "revisit")
	evt.Actor.Harness = "claude_cli"
	// Actor.SessionID deliberately left empty.
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := noticeCountFor(t, db, 9104); got != 1 {
		t.Errorf("an unattributed change must notify every holder, got %d notices", got)
	}
}

// TestStampTaskActor_TheEnvelopeDecidesNotTheProcess is E-2006 itself.
//
// Before it, the executor answered "was this an agent?" by reading
// os.Getenv("CLAUDECODE") in whatever process happened to be executing the
// event. The envelope had already begun recording the same fact as
// Actor.Harness (E-2005), so there were two answers to one question and they
// could disagree in BOTH directions. These two cases are that disagreement,
// and each asserts the opposite of what the old code did.
//
// The process environment here is set to the WRONG answer on purpose. Passing
// requires the executor to ignore it — which is the point: the envelope's value
// is the recorded one, so a notice that was dropped can be explained afterward
// from the ledger line, and a replayed or hand-rolled event carries its own
// answer instead of inheriting the executing process's.
func TestStampTaskActor_TheEnvelopeDecidesNotTheProcess(t *testing.T) {
	t.Run("a human's event executed inside an agent's process", func(t *testing.T) {
		db := withExecutorDB(t)
		seedHeldTask(t, db, 704, 9105)

		// Every signal the old gate and the harness detector key on says
		// "agent". The envelope says a person emitted this, and the envelope is
		// what was observed at emit time.
		t.Setenv("CLAUDECODE", "1")
		t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")

		evt := newStatusChangedEvent(t, 704, "ready", "revisit")
		evt.Actor.Harness = ""
		evt.Actor.SessionID = "9105"
		if _, err := events.Execute(evt, nil); err != nil {
			t.Fatalf("Execute: %v", err)
		}

		var actor sql.NullInt64
		if err := db.QueryRow(
			"SELECT changed_by_session FROM tasks WHERE id = 704").Scan(&actor); err != nil {
			t.Fatalf("reading actor: %v", err)
		}
		if actor.Valid {
			t.Errorf("stamped %d from the process environment; the envelope said no "+
				"harness, so a person made this change", actor.Int64)
		}
		if got := noticeCountFor(t, db, 9105); got != 1 {
			t.Errorf("the holding session must be told, got %d notices", got)
		}
	})

	t.Run("an agent's event executed outside an agent's process", func(t *testing.T) {
		db := withExecutorDB(t)
		seedHeldTask(t, db, 705, 9106)

		// The inverse: nothing in the environment says agent — a replay, a
		// daemon, a person's shell draining a queue — but the envelope recorded
		// the harness that actually emitted it.
		t.Setenv("CLAUDECODE", "")
		t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
		t.Setenv("__CFBundleIdentifier", "")

		evt := newStatusChangedEvent(t, 705, "ready", "revisit")
		evt.Actor.Harness = "claude_cli"
		evt.Actor.SessionID = "9106"
		if _, err := events.Execute(evt, nil); err != nil {
			t.Fatalf("Execute: %v", err)
		}

		var actor sql.NullInt64
		if err := db.QueryRow(
			"SELECT changed_by_session FROM tasks WHERE id = 705").Scan(&actor); err != nil {
			t.Fatalf("reading actor: %v", err)
		}
		if !actor.Valid || actor.Int64 != 9106 {
			t.Errorf("changed_by_session = %v, want 9106; the envelope recorded the "+
				"harness that emitted this, whatever process replayed it", actor)
		}
		if got := noticeCountFor(t, db, 9106); got != 0 {
			t.Errorf("the emitting agent must not be told about its own change, got %d notices", got)
		}
	})

	// An unsupported harness is still a harness. A Desktop session's own edit is
	// noise to itself exactly as a CLI session's is — which is why the predicate
	// is agentenv.Present, not agentenv.Supported.
	t.Run("an unsupported harness is still an agent", func(t *testing.T) {
		db := withExecutorDB(t)
		seedHeldTask(t, db, 706, 9107)

		evt := newStatusChangedEvent(t, 706, "ready", "revisit")
		evt.Actor.Harness = "claude_desktop"
		evt.Actor.SessionID = "9107"
		if _, err := events.Execute(evt, nil); err != nil {
			t.Fatalf("Execute: %v", err)
		}

		if got := noticeCountFor(t, db, 9107); got != 0 {
			t.Errorf("a Desktop agent must not be told about its own change, got %d notices", got)
		}
	})
}
