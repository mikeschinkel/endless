package events

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"testing"
)

// taskDepCreatedEvent / taskDepDeletedEvent build task_dep.* events for the
// executor + replay tests. The payload's source/target are already in storage
// (active-voice) order, mirroring what Python emits after resolving the swap.
func taskDepCreatedEvent(t *testing.T, srcID, tgtID int64, depType string, actor Actor) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskDepCreatedPayload{
		SourceID: srcID, TargetID: tgtID, DepType: depType,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-07-10T00:00:00",
		Kind:    KindTaskDepCreated,
		Project: "test",
		Entity:  EntityRef{Type: EntityTaskDep, ID: strconv.FormatInt(srcID, 10)},
		Actor:   actor,
		Payload: payload,
	}
}

func taskDepDeletedEvent(t *testing.T, srcID, tgtID int64, depType string, actor Actor) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskDepDeletedPayload{
		SourceID: srcID, TargetID: tgtID, DepType: depType,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-07-10T00:00:01",
		Kind:    KindTaskDepDeleted,
		Project: "test",
		Entity:  EntityRef{Type: EntityTaskDep, ID: strconv.FormatInt(srcID, 10)},
		Actor:   actor,
		Payload: payload,
	}
}

func countTaskDeps(t *testing.T, db *sql.DB, srcID, tgtID int64, depType string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM task_deps
		  WHERE source_type = 'task' AND source_id = ?
		    AND target_type = 'task' AND target_id = ? AND dep_type = ?`,
		srcID, tgtID, depType,
	).Scan(&n); err != nil {
		t.Fatalf("count task_deps: %v", err)
	}
	return n
}

// TestExecTaskDepCreated_RowAndTouch: linking writes the task_deps row and
// enrolls BOTH endpoints in session_tasks as 'revisited'.
func TestExecTaskDepCreated_RowAndTouch(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	if _, err := dispatch(db, taskDepCreatedEvent(t, 100, 101, "blocks", actor), nil); err != nil {
		t.Fatalf("dispatch task_dep.created: %v", err)
	}

	if got := countTaskDeps(t, db, 100, 101, "blocks"); got != 1 {
		t.Errorf("task_deps row count = %d, want 1", got)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "revisited" {
		t.Errorf("source endpoint relation = %q, want revisited", got)
	}
	if got := sessionTaskRelation(t, db, 42, 101); got != "revisited" {
		t.Errorf("target endpoint relation = %q, want revisited", got)
	}
}

// TestExecTaskDepCreated_NoSessionNoTouch: a session-less actor still writes
// the row but records no touch (shouldRecordSessionTouch is false).
func TestExecTaskDepCreated_NoSessionNoTouch(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSystem, ID: "cron"} // no SessionID

	if _, err := dispatch(db, taskDepCreatedEvent(t, 200, 201, "relates_to", actor), nil); err != nil {
		t.Fatalf("dispatch task_dep.created: %v", err)
	}

	if got := countTaskDeps(t, db, 200, 201, "relates_to"); got != 1 {
		t.Errorf("task_deps row count = %d, want 1", got)
	}
	if got := countSessionTasks(t, db); got != 0 {
		t.Errorf("session_tasks count = %d, want 0 (no session actor)", got)
	}
}

// TestExecTaskDepDeleted_RowAndTouch: unlinking removes the exact row (keyed on
// dep_type) and records the same revisited touch for both endpoints.
func TestExecTaskDepDeleted_RowAndTouch(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	// Two relations between the same pair; deleting 'blocks' must leave
	// 'relates_to' intact (dep_type keying).
	if _, err := dispatch(db, taskDepCreatedEvent(t, 100, 101, "blocks", Actor{Kind: ActorSystem, ID: "seed"}), nil); err != nil {
		t.Fatalf("seed blocks: %v", err)
	}
	if _, err := dispatch(db, taskDepCreatedEvent(t, 100, 101, "relates_to", Actor{Kind: ActorSystem, ID: "seed"}), nil); err != nil {
		t.Fatalf("seed relates_to: %v", err)
	}

	if _, err := dispatch(db, taskDepDeletedEvent(t, 100, 101, "blocks", actor), nil); err != nil {
		t.Fatalf("dispatch task_dep.deleted: %v", err)
	}

	if got := countTaskDeps(t, db, 100, 101, "blocks"); got != 0 {
		t.Errorf("blocks row count = %d, want 0", got)
	}
	if got := countTaskDeps(t, db, 100, 101, "relates_to"); got != 1 {
		t.Errorf("relates_to row count = %d, want 1 (must survive)", got)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "revisited" {
		t.Errorf("source endpoint relation = %q, want revisited", got)
	}
	if got := sessionTaskRelation(t, db, 42, 101); got != "revisited" {
		t.Errorf("target endpoint relation = %q, want revisited", got)
	}
}

// TestExecTaskDepDeleted_NoMatchErrors: deleting a nonexistent relation is a
// loud error (Go owns write integrity, no silent no-op).
func TestExecTaskDepDeleted_NoMatchErrors(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	_, err := dispatch(db, taskDepDeletedEvent(t, 300, 301, "blocks", actor), nil)
	if err == nil {
		t.Fatal("expected error deleting nonexistent task_dep, got nil")
	}
}

// TestReplayTaskDep_ReproducesRows: full-ledger replay reproduces the task_deps
// insert and delete (no drift vs the live executor) and writes NO session_tasks
// (live-only state).
func TestReplayTaskDep_ReproducesRows(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	if err := replayTaskDepCreated(db, taskDepCreatedEvent(t, 100, 101, "blocks", actor), &ProjectResult{}); err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if got := countTaskDeps(t, db, 100, 101, "blocks"); got != 1 {
		t.Errorf("after replay create: row count = %d, want 1", got)
	}
	if got := countSessionTasks(t, db); got != 0 {
		t.Errorf("replay must not write session_tasks; count = %d, want 0", got)
	}

	if err := replayTaskDepDeleted(db, taskDepDeletedEvent(t, 100, 101, "blocks", actor), &ProjectResult{}); err != nil {
		t.Fatalf("replay delete: %v", err)
	}
	if got := countTaskDeps(t, db, 100, 101, "blocks"); got != 0 {
		t.Errorf("after replay delete: row count = %d, want 0", got)
	}
}
