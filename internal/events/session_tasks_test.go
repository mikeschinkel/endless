package events

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

// newSessionTasksTestDB opens a file-backed SQLite DB in t.TempDir() and
// applies the V0 baseline schema plus the session_tasks table (V9). Seeds
// one project so task inserts satisfy their project_id FK. The connection
// is closed automatically on test cleanup.
func newSessionTasksTestDB(t *testing.T) *sql.DB {
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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_tasks (
		session_id INTEGER NOT NULL,
		task_id    INTEGER NOT NULL,
		created_at TEXT    NOT NULL,
		updated_at TEXT    NOT NULL,
		UNIQUE(session_id, task_id)
	)`); err != nil {
		t.Fatalf("create session_tasks: %v", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_session_tasks_task
		ON session_tasks(task_id)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-05-16T00:00:00', '2026-05-16T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

func taskCreatedEvent(t *testing.T, taskID int64, actor Actor) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskCreatedPayload{
		Title:  "probe",
		Phase:  "now",
		Status: "needs_plan",
		Type:   "task",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-05-16T00:00:00",
		Kind:    KindTaskCreated,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Actor:   actor,
		Payload: payload,
	}
}

func taskFieldsUpdatedEvent(t *testing.T, taskID int64, actor Actor) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskFieldsUpdatedPayload{
		Fields: map[string]any{"description": "updated"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-05-16T00:00:01",
		Kind:    KindTaskFieldsUpdated,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Actor:   actor,
		Payload: payload,
	}
}

func taskDeletedEvent(t *testing.T, taskID int64, actor Actor) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskDeletedPayload{Title: "probe"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-05-16T00:00:02",
		Kind:    KindTaskDeleted,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Actor:   actor,
		Payload: payload,
	}
}

func sessionTasksRow(t *testing.T, db *sql.DB, sessionID, taskID int64) (createdAt, updatedAt string, found bool) {
	t.Helper()
	err := db.QueryRow(
		"SELECT created_at, updated_at FROM session_tasks WHERE session_id = ? AND task_id = ?",
		sessionID, taskID,
	).Scan(&createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return "", "", false
	}
	if err != nil {
		t.Fatalf("query session_tasks: %v", err)
	}
	return createdAt, updatedAt, true
}

func countSessionTasks(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM session_tasks").Scan(&n); err != nil {
		t.Fatalf("count session_tasks: %v", err)
	}
	return n
}

// TestSessionTasks_CreatedBySession verifies that a task.created event
// from an ActorSession produces exactly one session_tasks row with
// created_at == updated_at.
func TestSessionTasks_CreatedBySession(t *testing.T) {
	db := newSessionTasksTestDB(t)

	evt := taskCreatedEvent(t, 100, Actor{Kind: ActorSession, ID: "s1", SessionID: "42"})
	if _, err := dispatch(db, evt); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if n := countSessionTasks(t, db); n != 1 {
		t.Fatalf("expected 1 session_tasks row, got %d", n)
	}
	createdAt, updatedAt, found := sessionTasksRow(t, db, 42, 100)
	if !found {
		t.Fatal("expected row (42, 100) to exist")
	}
	if createdAt != updatedAt {
		t.Errorf("expected created_at == updated_at on insert; got created=%q updated=%q", createdAt, updatedAt)
	}
}

// TestSessionTasks_UpdateAdvancesUpdatedAt verifies that a subsequent
// task.fields_updated for the same (session, task) pair bumps updated_at
// while leaving created_at unchanged. The second call uses a future
// timestamp to guarantee monotonic advancement regardless of clock
// resolution.
func TestSessionTasks_UpdateAdvancesUpdatedAt(t *testing.T) {
	db := newSessionTasksTestDB(t)

	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}
	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor)); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	createdAt1, updatedAt1, _ := sessionTasksRow(t, db, 42, 100)

	// Bypass the now() call's second-resolution by manually backdating
	// created/updated so the update produces a visibly later updated_at.
	if _, err := db.Exec(
		"UPDATE session_tasks SET created_at = ?, updated_at = ? WHERE session_id = ? AND task_id = ?",
		"2026-05-16T00:00:00", "2026-05-16T00:00:00", 42, 100,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor)); err != nil {
		t.Fatalf("dispatch update: %v", err)
	}

	createdAt2, updatedAt2, _ := sessionTasksRow(t, db, 42, 100)
	if createdAt2 != "2026-05-16T00:00:00" {
		t.Errorf("created_at must not change on conflict; was %q then %q after update", createdAt1, createdAt2)
	}
	if updatedAt2 <= "2026-05-16T00:00:00" {
		t.Errorf("updated_at must advance on conflict; was %q then %q after update", updatedAt1, updatedAt2)
	}
}

// TestSessionTasks_NoRowForCLIActor verifies the strict ActorSession
// guard: a CLI actor carrying a session_id does NOT produce a row.
func TestSessionTasks_NoRowForCLIActor(t *testing.T) {
	db := newSessionTasksTestDB(t)

	evt := taskCreatedEvent(t, 100, Actor{Kind: ActorCLI, ID: "user@host", SessionID: "42"})
	if _, err := dispatch(db, evt); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if n := countSessionTasks(t, db); n != 0 {
		t.Errorf("expected 0 session_tasks rows for ActorCLI even with SessionID set, got %d", n)
	}
}

// TestSessionTasks_NoRowForEmptySessionID verifies the empty-string guard:
// an ActorSession with no SessionID does NOT produce a row.
func TestSessionTasks_NoRowForEmptySessionID(t *testing.T) {
	db := newSessionTasksTestDB(t)

	evt := taskCreatedEvent(t, 100, Actor{Kind: ActorSession, ID: "s1", SessionID: ""})
	if _, err := dispatch(db, evt); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if n := countSessionTasks(t, db); n != 0 {
		t.Errorf("expected 0 session_tasks rows for empty SessionID, got %d", n)
	}
}

// TestSessionTasks_TwoSessionsSameTask verifies that two different
// sessions each create their own session_tasks row for the same task.
func TestSessionTasks_TwoSessionsSameTask(t *testing.T) {
	db := newSessionTasksTestDB(t)

	if _, err := dispatch(db, taskCreatedEvent(t, 100, Actor{Kind: ActorSession, ID: "s1", SessionID: "42"})); err != nil {
		t.Fatalf("dispatch s1: %v", err)
	}
	// Different session, same task — uses task.fields_updated since
	// task.created on an existing id would fail the INSERT.
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, Actor{Kind: ActorSession, ID: "s2", SessionID: "43"})); err != nil {
		t.Fatalf("dispatch s2: %v", err)
	}

	if n := countSessionTasks(t, db); n != 2 {
		t.Errorf("expected 2 session_tasks rows for two sessions touching same task, got %d", n)
	}
	if _, _, ok := sessionTasksRow(t, db, 42, 100); !ok {
		t.Error("missing row (42, 100)")
	}
	if _, _, ok := sessionTasksRow(t, db, 43, 100); !ok {
		t.Error("missing row (43, 100)")
	}
}

// TestSessionTasks_OneSessionTwoTasks verifies that one session touching
// two different tasks produces two rows.
func TestSessionTasks_OneSessionTwoTasks(t *testing.T) {
	db := newSessionTasksTestDB(t)

	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}
	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor)); err != nil {
		t.Fatalf("dispatch task 100: %v", err)
	}
	if _, err := dispatch(db, taskCreatedEvent(t, 101, actor)); err != nil {
		t.Fatalf("dispatch task 101: %v", err)
	}

	if n := countSessionTasks(t, db); n != 2 {
		t.Errorf("expected 2 session_tasks rows for one session touching two tasks, got %d", n)
	}
}

// TestSessionTasks_IdempotentReplay verifies that re-applying the same
// (session, task) touch via task.fields_updated keeps the row count at
// one — the ON CONFLICT clause must not multiply rows.
func TestSessionTasks_IdempotentReplay(t *testing.T) {
	db := newSessionTasksTestDB(t)

	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}
	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor)); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor)); err != nil {
		t.Fatalf("dispatch update 1: %v", err)
	}
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor)); err != nil {
		t.Fatalf("dispatch update 2: %v", err)
	}

	if n := countSessionTasks(t, db); n != 1 {
		t.Errorf("expected 1 session_tasks row after replay, got %d", n)
	}
}

// TestSessionTasks_DeletedTaskRetainsRow verifies the no-FK design: a
// task.deleted records a session_tasks row, and the row survives the
// subsequent DELETE FROM tasks. If session_tasks had a FK on task_id
// with ON DELETE CASCADE, this test would fail.
func TestSessionTasks_DeletedTaskRetainsRow(t *testing.T) {
	db := newSessionTasksTestDB(t)

	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}
	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor)); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if _, err := dispatch(db, taskDeletedEvent(t, 100, actor)); err != nil {
		t.Fatalf("dispatch delete: %v", err)
	}

	if n := countSessionTasks(t, db); n != 1 {
		t.Errorf("expected session_tasks row to survive task deletion, got count=%d", n)
	}
	if _, _, ok := sessionTasksRow(t, db, 42, 100); !ok {
		t.Error("expected (42, 100) row to remain after task deletion")
	}

	// And confirm the task itself is gone.
	var taskCount int
	if err := db.QueryRow("SELECT count(*) FROM tasks WHERE id = ?", 100).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 0 {
		t.Errorf("expected task 100 to be deleted, found %d row(s)", taskCount)
	}
}
