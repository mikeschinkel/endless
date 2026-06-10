// Black-box tests for the executor entry points (PreAllocateTaskID,
// BeginImmediate, Execute). These exercise the monitor.DB() seam via
// monitor.SetTestDB (E-1506): each test stands up a fresh schema-applied
// SQLite DB, rebinds monitor.DB() to it for the lifetime of t, seeds the
// minimal rows the executors need, and asserts the resulting state.
//
// Concurrency: SetTestDB mutates package-level state in monitor; no
// t.Parallel() in this file.
package events_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// withExecutorDB stands up a fresh schema-applied SQLite DB, rebinds
// monitor.DB() to it for the lifetime of t, seeds a "test" project, and
// returns the handle for test-level inspection.
func withExecutorDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("set fks: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-05-30T00:00:00', '2026-05-30T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)
	return db
}

// TestPreAllocateTaskID_MonotonicallyIncreasing verifies that successive
// PreAllocateTaskID calls return distinct, increasing IDs once each prior
// transaction is finalized via execAndCommit on an actual task.created
// event.
func TestPreAllocateTaskID_MonotonicallyIncreasing(t *testing.T) {
	withExecutorDB(t)

	id1, execAndCommit1, _, err := events.PreAllocateTaskID()
	if err != nil {
		t.Fatalf("PreAllocateTaskID 1: %v", err)
	}
	if id1 != 1 {
		t.Errorf("first id = %d, want 1", id1)
	}

	evt1 := newTaskCreatedEvent(t, id1, "first")
	if _, err := execAndCommit1(evt1); err != nil {
		t.Fatalf("execAndCommit 1: %v", err)
	}

	id2, execAndCommit2, _, err := events.PreAllocateTaskID()
	if err != nil {
		t.Fatalf("PreAllocateTaskID 2: %v", err)
	}
	if id2 <= id1 {
		t.Errorf("second id = %d, want > %d", id2, id1)
	}
	evt2 := newTaskCreatedEvent(t, id2, "second")
	if _, err := execAndCommit2(evt2); err != nil {
		t.Fatalf("execAndCommit 2: %v", err)
	}
}

// TestPreAllocateTaskID_RollbackReleasesLockForNextCaller verifies that
// after calling rollback() instead of execAndCommit(), the next
// PreAllocateTaskID call succeeds (the lock was released) and observes
// the same next-id (no row was actually inserted).
func TestPreAllocateTaskID_RollbackReleasesLockForNextCaller(t *testing.T) {
	withExecutorDB(t)

	id1, _, rollback1, err := events.PreAllocateTaskID()
	if err != nil {
		t.Fatalf("PreAllocateTaskID 1: %v", err)
	}
	rollback1()

	// After rollback, the next caller succeeds.
	id2, _, rollback2, err := events.PreAllocateTaskID()
	if err != nil {
		t.Fatalf("PreAllocateTaskID after rollback: %v", err)
	}
	rollback2()

	// And because no row was inserted, the id stays at the same value.
	if id2 != id1 {
		t.Errorf("after rollback, next id = %d, want %d (no row committed)", id2, id1)
	}
}

// TestBeginImmediate_CommitAndRollback verifies the two flow paths of
// BeginImmediate: execAndCommit applies the event and releases the lock;
// rollback releases the lock without writing.
func TestBeginImmediate_CommitAndRollback(t *testing.T) {
	db := withExecutorDB(t)

	// Path A: commit flow with a status_change against a pre-existing task.
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id)
		 VALUES (100, 1, 'preexisting', 'now', 'ready', 1)`,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	execAndCommit, _, err := events.BeginImmediate()
	if err != nil {
		t.Fatalf("BeginImmediate (commit path): %v", err)
	}
	evt := newStatusChangedEvent(t, 100, "ready", "in_progress")
	if _, err := execAndCommit(evt); err != nil {
		t.Fatalf("execAndCommit: %v", err)
	}

	var status string
	if err := db.QueryRow("SELECT status FROM tasks WHERE id = ?", 100).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "in_progress" {
		t.Errorf("status = %q, want in_progress", status)
	}

	// Path B: rollback releases the lock so the next BeginImmediate succeeds.
	_, rollback, err := events.BeginImmediate()
	if err != nil {
		t.Fatalf("BeginImmediate (rollback path): %v", err)
	}
	rollback()

	if _, _, err := events.BeginImmediate(); err != nil {
		t.Fatalf("BeginImmediate after rollback should succeed: %v", err)
	}
	// Clean up the still-open tx so SetTestDB cleanup is safe.
	db.Exec("ROLLBACK")
}

// TestExecute_TaskCreatedInsertsRow verifies the end-to-end Execute path
// against a task.created event: the dispatcher routes to execTaskCreated,
// which inserts a tasks row inside a new transaction and commits.
func TestExecute_TaskCreatedInsertsRow(t *testing.T) {
	db := withExecutorDB(t)

	evt := newTaskCreatedEvent(t, 500, "exec-target")
	res, err := events.Execute(evt)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil {
		t.Fatal("Execute returned nil result")
	}
	if res.TaskID != 500 {
		t.Errorf("result.TaskID = %d, want 500", res.TaskID)
	}

	var (
		title  string
		status string
	)
	if err := db.QueryRow(
		"SELECT title, status FROM tasks WHERE id = ?", 500,
	).Scan(&title, &status); err != nil {
		t.Fatalf("query inserted task: %v", err)
	}
	if title != "exec-target" {
		t.Errorf("title = %q, want exec-target", title)
	}
	if status != "needs_plan" {
		t.Errorf("status = %q, want needs_plan", status)
	}
}

// newTaskCreatedEvent builds a minimal valid task.created Event with the
// given entity id and title, targeting the "test" project seeded by
// withExecutorDB. Used by the PreAllocateTaskID and Execute tests.
func newTaskCreatedEvent(t *testing.T, id int64, title string) *events.Event {
	t.Helper()
	payload, err := json.Marshal(events.TaskCreatedPayload{
		Title:  title,
		Phase:  "now",
		Status: "needs_plan",
		Type:   "task",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &events.Event{
		V:       events.Version,
		TS:      "5WYM00000001",
		Kind:    events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(id)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
}

// newStatusChangedEvent builds a minimal valid task.status_changed Event.
func newStatusChangedEvent(t *testing.T, id int64, oldStatus, newStatus string) *events.Event {
	t.Helper()
	payload, err := json.Marshal(events.TaskStatusChangedPayload{
		OldStatus: oldStatus,
		NewStatus: newStatus,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &events.Event{
		V:       events.Version,
		TS:      "5WYM00000002",
		Kind:    events.KindTaskStatusChanged,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(id)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
}

// itoaInt64 formats an int64 as a decimal string. Local helper rather
// than strconv import on top of all the others.
func itoaInt64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
