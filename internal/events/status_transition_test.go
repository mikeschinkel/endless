// White-box tests for the status-lifecycle guard (E-2018). They call
// execTaskFieldsUpdated directly against a fresh schema-applied SQLite DB, which
// is what lets them seed a sessions row and drive the actor half — the half that
// cannot be exercised from the table alone.
package events

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/agentenv"
	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// harnessed is the Harness value an agent-emitted event carries. Spelled from
// agentenv rather than as a literal so it cannot drift from what
// events.DetectedHarness actually stamps.
var harnessed = string(agentenv.ClaudeCLI)

// seedHolderSession inserts a sessions row bound to a task. A taskID of 0
// leaves sessions.task_id NULL — a session that has claimed nothing. Distinct
// from seedSession (session_tasks_order_test.go), which seeds a session with no
// task binding at all.
func seedHolderSession(t *testing.T, db *sql.DB, id int64, taskID int64) {
	t.Helper()
	var bound any
	if taskID != 0 {
		bound = taskID
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, task_id, started_at)
		 VALUES (?, ?, 1, ?, '2026-06-21T00:00:00')`,
		id, "sess-"+taskIDString(id), bound,
	); err != nil {
		t.Fatalf("seed session %d: %v", id, err)
	}
}

// statusUpdate builds a task.fields_updated event setting a status, attributed
// to an agent-harnessed actor with the given session id.
func statusUpdate(t *testing.T, taskID int64, status, sessionID string) *Event {
	t.Helper()
	return fieldsUpdate(t, taskID, map[string]any{"status": status}, Actor{
		Kind: ActorCLI, ID: "tester", SessionID: sessionID, Harness: harnessed,
	})
}

func fieldsUpdate(t *testing.T, taskID int64, fields map[string]any, actor Actor) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskFieldsUpdatedPayload{Fields: fields})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       Version,
		TS:      "5WYM00000003",
		Kind:    KindTaskFieldsUpdated,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: taskIDString(taskID)},
		Actor:   actor,
		Payload: payload,
	}
}

// ---------------------------------------------------------------------------
// The reported case
// ---------------------------------------------------------------------------

// TestReportedCase_UnplannedNeverClaimedToUnverified is the defect verbatim: a
// session holding one task set an `unplanned`, never-claimed task to
// `unverified`, leaving a task that claimed to be implemented with no worktree
// and nowhere for `session goto` to go. Both halves of the guard refuse it
// independently; this asserts the write does not land, whichever fires first.
func TestReportedCase_UnplannedNeverClaimedToUnverified(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1817, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)
	seedTask(t, db, 2015, nil, int(tasktype.TaskTypeTask), taskstatus.Unplanned)
	seedHolderSession(t, db, 1124, 1817)

	_, err := execTaskFieldsUpdated(db, statusUpdate(t, 2015, taskstatus.Unverified, "1124"), nil)
	if err == nil {
		t.Fatal("the reported case was accepted")
	}
	if got, _ := taskStatus(t, db, 2015); got != taskstatus.Unplanned {
		t.Errorf("task 2015 status = %q, want it untouched at %q", got, taskstatus.Unplanned)
	}
}

// ---------------------------------------------------------------------------
// Transition legality
// ---------------------------------------------------------------------------

// TestEveryTableEdgePasses walks the table rather than hand-listing: an edge
// added tomorrow is covered with no edit here, which is the property that
// stops the guard and the table from drifting apart.
func TestEveryTableEdgePasses(t *testing.T) {
	for _, tr := range taskstatus.Transitions() {
		tt := tasktype.TaskTypeTask
		if len(tr.Types) > 0 {
			tt = tr.Types[0]
		}
		if err := ValidateStatusTransition(tr.From, tr.To, tt); err != nil {
			t.Errorf("table edge %q -> %q refused: %v", tr.From, tr.To, err)
		}
	}
}

// TestEveryAbsentEdgeIsRefused is the complement, also walked: every ordered
// pair the table does NOT contain must be refused. Without this the guard could
// silently allow everything and every other test would still pass.
func TestEveryAbsentEdgeIsRefused(t *testing.T) {
	all := taskstatus.Get(taskstatus.All)
	for _, from := range all {
		for _, to := range all {
			if from == to {
				continue // a no-op write, allowed by rule
			}
			want := taskstatus.TransitionAllowed(from, to, tasktype.TaskTypeTask)
			err := ValidateStatusTransition(from, to, tasktype.TaskTypeTask)
			if want && err != nil {
				t.Errorf("%q -> %q is in the table but was refused: %v", from, to, err)
			}
			if !want && err == nil {
				t.Errorf("%q -> %q is not in the table but was accepted", from, to)
			}
		}
	}
}

// TestRefusalNamesTheWayOut pins the half of the message that matters most to
// the caller — usually an agent, which told only "no" will guess again.
func TestRefusalNamesTheWayOut(t *testing.T) {
	err := ValidateStatusTransition(taskstatus.Unplanned, taskstatus.Unverified, tasktype.TaskTypeTask)
	if err == nil {
		t.Fatal("unplanned -> unverified was accepted")
	}
	msg := err.Error()
	for _, want := range []string{"unplanned", "unverified", "submitted", "underway"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q: %s", want, msg)
		}
	}
}

// TestUnrelatedEditIsNotJudged pins that the guard only fires when a status is
// actually being written. A row already sitting somewhere the table dislikes
// must stay editable — otherwise pre-rule data becomes read-only.
func TestUnrelatedEditIsNotJudged(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 10, nil, int(tasktype.TaskTypeTask), "blocked")

	evt := fieldsUpdate(t, 10, map[string]any{"title": "renamed"}, Actor{
		Kind: ActorCLI, ID: "tester", SessionID: "", Harness: harnessed,
	})
	if _, err := execTaskFieldsUpdated(db, evt, nil); err != nil {
		t.Fatalf("a title-only edit on a retired-status row was refused: %v", err)
	}
}

// TestTypeChangeInTheSameUpdateWinsTheLane pins that a call which changes both
// the type and the status is judged against the type it ENDS on — the write
// lands as one row, so the lane it is judged against must be the lane it
// arrives in.
func TestTypeChangeInTheSameUpdateWinsTheLane(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 20, nil, int(tasktype.TaskTypeResearch), taskstatus.Underway)
	seedHolderSession(t, db, 900, 20)

	evt := fieldsUpdate(t, 20, map[string]any{
		"type": "todo", "status": taskstatus.Unverified,
	}, Actor{Kind: ActorCLI, ID: "tester", SessionID: "900", Harness: harnessed})
	if _, err := execTaskFieldsUpdated(db, evt, nil); err != nil {
		t.Fatalf("research -> todo + unverified in one update was refused: %v", err)
	}
	if got, _ := taskStatus(t, db, 20); got != taskstatus.Unverified {
		t.Errorf("status = %q, want unverified", got)
	}
}

// ---------------------------------------------------------------------------
// Actor reality
// ---------------------------------------------------------------------------

func TestSessionMayNotAdvanceATaskItDoesNotHold(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 30, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)
	seedTask(t, db, 31, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)
	seedHolderSession(t, db, 901, 31) // holds a different task

	_, err := execTaskFieldsUpdated(db, statusUpdate(t, 30, taskstatus.Unverified, "901"), nil)
	if err == nil {
		t.Fatal("a session advanced a task it does not hold")
	}
	if !strings.Contains(err.Error(), "holds task 31") {
		t.Errorf("refusal does not name the task the session actually holds: %v", err)
	}
}

func TestSessionThatHoldsTheTaskMayAdvanceIt(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 40, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)
	seedHolderSession(t, db, 902, 40)

	if _, err := execTaskFieldsUpdated(db, statusUpdate(t, 40, taskstatus.Unverified, "902"), nil); err != nil {
		t.Fatalf("the holding session was refused: %v", err)
	}
	if got, _ := taskStatus(t, db, 40); got != taskstatus.Unverified {
		t.Errorf("status = %q, want unverified", got)
	}
}

func TestSessionHoldingNothingMayNotAdvanceATask(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 50, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)
	seedHolderSession(t, db, 903, 0) // claimed nothing

	_, err := execTaskFieldsUpdated(db, statusUpdate(t, 50, taskstatus.Unverified, "903"), nil)
	if err == nil {
		t.Fatal("a session that has claimed nothing advanced a task")
	}
	if !strings.Contains(err.Error(), "has not claimed any task") {
		t.Errorf("refusal does not say why: %v", err)
	}
}

// TestUnverifiedRequiresTheTaskToHaveBeenClaimed covers the case the
// held-by-this-session rule cannot: an agent-harnessed caller with no session
// id at all, reporting implementation on work nobody ever picked up.
func TestUnverifiedRequiresTheTaskToHaveBeenClaimed(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 60, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)

	_, err := execTaskFieldsUpdated(db, statusUpdate(t, 60, taskstatus.Unverified, ""), nil)
	if err == nil {
		t.Fatal("`unverified` landed on a task no session ever claimed")
	}
	if !strings.Contains(err.Error(), "never been claimed") {
		t.Errorf("refusal does not say why: %v", err)
	}
}

// TestStatusesOutsideTheWorkLaneAreNotActorChecked pins the rule's scope. Only
// `underway` and `unverified` assert something about who did the work; the
// others are judgments anyone may record.
func TestStatusesOutsideTheWorkLaneAreNotActorChecked(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 70, nil, int(tasktype.TaskTypeTask), taskstatus.Ready)
	seedTask(t, db, 71, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)
	seedHolderSession(t, db, 904, 71) // holds something else entirely

	if _, err := execTaskFieldsUpdated(db, statusUpdate(t, 70, taskstatus.Declined, "904"), nil); err != nil {
		t.Fatalf("declining someone else's task was refused: %v", err)
	}
}

// TestNoHarnessIsExempt is the E-2005 keying, asserted directly: this guards
// against an agent's mistake, not against a person. A person at a shell carries
// no harness — and routinely carries a NEIGHBOURING session's id, which is
// exactly why the exemption cannot be keyed on session-presence.
func TestNoHarnessIsExempt(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 80, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)
	seedTask(t, db, 81, nil, int(tasktype.TaskTypeTask), taskstatus.Underway)
	seedHolderSession(t, db, 905, 81) // the agent in the pane next door

	evt := fieldsUpdate(t, 80, map[string]any{"status": taskstatus.Unverified}, Actor{
		Kind: ActorCLI, ID: "mike", SessionID: "905", Harness: "",
	})
	if _, err := execTaskFieldsUpdated(db, evt, nil); err != nil {
		t.Fatalf("a person at a shell was refused: %v", err)
	}
	if got, _ := taskStatus(t, db, 80); got != taskstatus.Unverified {
		t.Errorf("status = %q, want unverified", got)
	}
}

// TestTransitionLegalityStillAppliesToAPerson pins that the exemption is
// narrow: it covers actor reality only. An illegal EDGE is a mistake whoever
// typed it.
func TestTransitionLegalityStillAppliesToAPerson(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 90, nil, int(tasktype.TaskTypeTask), taskstatus.Unplanned)

	evt := fieldsUpdate(t, 90, map[string]any{"status": taskstatus.Unverified}, Actor{
		Kind: ActorCLI, ID: "mike", SessionID: "", Harness: "",
	})
	if _, err := execTaskFieldsUpdated(db, evt, nil); err == nil {
		t.Fatal("unplanned -> unverified was accepted because a person typed it")
	}
}

// ---------------------------------------------------------------------------
// The inferred status writes
// ---------------------------------------------------------------------------

// TestPlanAttachPromotionPassesTheGuard pins that the executor's own inferred
// status write goes through the same validation and survives it. The promotion
// used to be the one status write with no explicit `status` field, so routing
// it through the guard is what stops a second unguarded write path forming —
// the shape that let a parent cycle through `task update --parent`.
func TestPlanAttachPromotionPassesTheGuard(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 100, nil, int(tasktype.TaskTypeTask), taskstatus.Untriaged)

	evt := fieldsUpdate(t, 100, map[string]any{"text": "# a plan"}, Actor{
		Kind: ActorCLI, ID: "tester", SessionID: "", Harness: harnessed,
	})
	if _, err := execTaskFieldsUpdated(db, evt, nil); err != nil {
		t.Fatalf("attaching a plan was refused: %v", err)
	}
	if got, _ := taskStatus(t, db, 100); got != taskstatus.Submitted {
		t.Errorf("status = %q, want submitted", got)
	}
}

// TestKeepStatusPinIsANoOpWrite pins the --keep-status mechanism: it sends the
// CURRENT status so the executor's promotion stands down, and the guard must
// read that as the no-op it is.
func TestKeepStatusPinIsANoOpWrite(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 110, nil, int(tasktype.TaskTypeTask), taskstatus.Unplanned)

	evt := fieldsUpdate(t, 110, map[string]any{
		"text": "# a plan", "status": taskstatus.Unplanned,
	}, Actor{Kind: ActorCLI, ID: "tester", SessionID: "", Harness: harnessed})
	if _, err := execTaskFieldsUpdated(db, evt, nil); err != nil {
		t.Fatalf("--keep-status's pinned write was refused: %v", err)
	}
	if got, _ := taskStatus(t, db, 110); got != taskstatus.Unplanned {
		t.Errorf("status = %q, want unplanned", got)
	}
}

// ---------------------------------------------------------------------------
// The legal path, end to end
// ---------------------------------------------------------------------------

// TestTheHappyPathRunsUnimpeded walks a task the whole way through `task
// update --status`, which is the check that matters most: a guard that refuses
// the ordinary workflow is worse than no guard.
func TestTheHappyPathRunsUnimpeded(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 120, nil, int(tasktype.TaskTypeTask), taskstatus.Untriaged)
	seedHolderSession(t, db, 906, 120)

	for _, status := range []string{
		taskstatus.Unplanned, taskstatus.Submitted, taskstatus.Ready,
		taskstatus.Underway, taskstatus.Unverified, taskstatus.Confirmed,
	} {
		if _, err := execTaskFieldsUpdated(db, statusUpdate(t, 120, status, "906"), nil); err != nil {
			t.Fatalf("the happy path stalled at %q: %v", status, err)
		}
		if got, _ := taskStatus(t, db, 120); got != status {
			t.Fatalf("status = %q, want %q", got, status)
		}
	}
}

// TestFindingsPathRunsUnimpeded is the other lane: research work terminates via
// `completed` and never enters verification. E-2016 put `unreviewed` in the
// path — the outcome is written, then the owner reads it — so the walk is one
// step longer than it was.
func TestFindingsPathRunsUnimpeded(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 130, nil, int(tasktype.TaskTypeResearch), taskstatus.Untriaged)
	seedHolderSession(t, db, 907, 130)

	for _, status := range []string{
		taskstatus.Submitted, taskstatus.Ready, taskstatus.Underway,
		taskstatus.Unreviewed, taskstatus.Completed,
	} {
		if _, err := execTaskFieldsUpdated(db, statusUpdate(t, 130, status, "907"), nil); err != nil {
			t.Fatalf("the findings path stalled at %q: %v", status, err)
		}
	}
}

// TestFindingsPathCannotSkipTheReviewGate is E-2016's guard at the executor,
// where the reported defect actually lands: a session that marks its own
// research outcome `completed` is refused, and told where to go instead.
func TestFindingsPathCannotSkipTheReviewGate(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 131, nil, int(tasktype.TaskTypeResearch), taskstatus.Untriaged)
	seedHolderSession(t, db, 908, 131)

	for _, status := range []string{
		taskstatus.Submitted, taskstatus.Ready, taskstatus.Underway,
	} {
		if _, err := execTaskFieldsUpdated(db, statusUpdate(t, 131, status, "908"), nil); err != nil {
			t.Fatalf("setup stalled at %q: %v", status, err)
		}
	}

	_, err := execTaskFieldsUpdated(db, statusUpdate(t, 131, taskstatus.Completed, "908"), nil)
	if err == nil {
		t.Fatal("a research task went straight to `completed`, skipping the review gate")
	}
	// The refusal has to name the route, or the session has no next move.
	if !strings.Contains(err.Error(), taskstatus.Unreviewed) {
		t.Errorf("refusal does not name `unreviewed` as the way forward: %v", err)
	}
	if got, _ := taskStatus(t, db, 131); got != taskstatus.Underway {
		t.Errorf("status = %q after a refused change, want it left at %q", got, taskstatus.Underway)
	}
}
