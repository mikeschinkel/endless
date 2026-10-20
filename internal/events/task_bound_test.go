package events

import (
	"database/sql"
	"testing"
)

// taskBoundEvent is taskClaimedEvent with the bind kind: the two carry the same
// payload and differ only in what they record (E-2291).
func taskBoundEvent(t *testing.T, taskID int64, actor Actor) *Event {
	t.Helper()
	evt := taskClaimedEvent(t, taskID, actor)
	evt.Kind = KindTaskBound
	return evt
}

func boundTaskID(t *testing.T, db *sql.DB, sessionID int64) int64 {
	t.Helper()
	var taskID sql.NullInt64
	if err := db.QueryRow(
		`SELECT task_id FROM sessions WHERE id = ?`, sessionID,
	).Scan(&taskID); err != nil {
		t.Fatalf("read sessions.task_id: %v", err)
	}
	return taskID.Int64
}

// TestTaskBound_OwnsWithoutClaiming is task.bound's contract (E-2291): the
// session becomes the task's owner — sessions.task_id, exactly as a claim sets
// it — and session_tasks records `bound`, so the bind is never read back as a
// claim.
func TestTaskBound_OwnsWithoutClaiming(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	actor := Actor{Kind: ActorCLI, ID: "u@h", SessionID: "42"}

	if _, err := dispatch(db, taskBoundEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch bound: %v", err)
	}
	if got := boundTaskID(t, db, 42); got != 100 {
		t.Errorf("sessions.task_id = %d, want 100", got)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "bound" {
		t.Errorf("relation = %q, want bound", got)
	}
	if got := sessionFocus(t, db, 42); got != 100 {
		t.Errorf("focus = %d, want 100 (a bind moves focus to the bound task)", got)
	}
}

// TestTaskBound_ClaimAfterBindUpgrades pins the route the claim refusal now
// offers: bind while the plan is worked out, then claim once it is attached.
// The claim must upgrade the row to `claimed` through the ladder.
func TestTaskBound_ClaimAfterBindUpgrades(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	actor := Actor{Kind: ActorCLI, ID: "u@h", SessionID: "42"}

	if _, err := dispatch(db, taskBoundEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch bound: %v", err)
	}
	if _, err := dispatch(db, taskClaimedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch claimed: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "claimed" {
		t.Errorf("relation = %q, want claimed", got)
	}
}

// TestTaskBound_UpgradesTouched pins the other rung: binding a task the session
// had only touched upgrades it to `bound`, and a bind never demotes a claim.
func TestTaskBound_UpgradesTouched(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	actor := Actor{Kind: ActorCLI, ID: "u@h", SessionID: "42"}

	if _, err := dispatch(db, membershipEvent(
		t, KindSessionTasksTouched, 42, []string{"E-100"}), nil,
	); err != nil {
		t.Fatalf("dispatch touched: %v", err)
	}
	if _, err := dispatch(db, taskBoundEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch bound: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "bound" {
		t.Errorf("relation = %q, want bound", got)
	}
}

// TestTaskBound_DoesNotDemoteClaimed pins that a bind after a claim — the
// idempotent re-bind the CLI short-circuits, but a replayed or hand-emitted
// event can still deliver — leaves the claim standing.
func TestTaskBound_DoesNotDemoteClaimed(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	actor := Actor{Kind: ActorCLI, ID: "u@h", SessionID: "42"}

	if _, err := dispatch(db, taskClaimedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch claimed: %v", err)
	}
	if _, err := dispatch(db, taskBoundEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch bound: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "claimed" {
		t.Errorf("relation = %q, want claimed", got)
	}
}
