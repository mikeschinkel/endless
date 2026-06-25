package events

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

// newPendingTestDB stands up a fresh schema-applied SQLite DB and seeds the
// minimum rows the autoAddUrgentPending tests need: one project, two
// sessions. Foreign keys are enabled so FK violations surface as test
// failures instead of silently-orphan rows.
func newPendingTestDB(t *testing.T) *sql.DB {
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
		 VALUES (1, 'test', '/tmp/test', 'active',
		         '2026-06-11T00:00:00', '2026-06-11T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	for _, id := range []int{41, 42} {
		if _, err := db.Exec(
			`INSERT INTO sessions (id, session_id, project_id, started_at)
			 VALUES (?, ?, 1, '2026-06-11T00:00:00')`,
			id, "sess-"+itoa(id),
		); err != nil {
			t.Fatalf("seed session %d: %v", id, err)
		}
	}
	return db
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
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

func taskCreatedUrgentEvent(t *testing.T, taskID int64, sessionID string) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskCreatedPayload{
		Title:  "urgent task",
		Phase:  "urgent",
		Status: "unplanned",
		Type:   "task",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       Version,
		TS:      "5WYM00000001",
		Kind:    KindTaskCreated,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: taskIDString(taskID)},
		Actor:   Actor{Kind: ActorCLI, ID: "tester", SessionID: sessionID},
		Payload: payload,
	}
}

func taskCreatedNonUrgentEvent(t *testing.T, taskID int64, sessionID string) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskCreatedPayload{
		Title:  "regular task",
		Phase:  "now",
		Status: "unplanned",
		Type:   "task",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       Version,
		TS:      "5WYM00000002",
		Kind:    KindTaskCreated,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: taskIDString(taskID)},
		Actor:   Actor{Kind: ActorCLI, ID: "tester", SessionID: sessionID},
		Payload: payload,
	}
}

func taskFieldsUpdatedPhaseEvent(t *testing.T, taskID int64, newPhase, sessionID string) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskFieldsUpdatedPayload{
		Fields: map[string]any{"phase": newPhase},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       Version,
		TS:      "5WYM00000003",
		Kind:    KindTaskFieldsUpdated,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: taskIDString(taskID)},
		Actor:   Actor{Kind: ActorCLI, ID: "tester", SessionID: sessionID},
		Payload: payload,
	}
}

// pendingCount returns the number of project_next_pending rows for the
// given (project_next_id, task_id) — used to assert idempotency.
func pendingCount(t *testing.T, db *sql.DB, taskID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM project_next_pending WHERE task_id = ?`,
		taskIDString(taskID),
	).Scan(&n); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	return n
}

// eventCount returns the number of pending.added events recorded for the
// given task_id.
func pendingAddedEventCount(t *testing.T, db *sql.DB, taskID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM project_next_events
		 WHERE kind = 'pending.added' AND payload LIKE ?`,
		`%"task_id":"`+taskIDString(taskID)+`"%`,
	).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

// Scenario 1: task.created with phase=urgent inserts exactly one pending row
// and emits exactly one pending.added event with the canonical reason.
func TestAutoAddUrgentPending_TaskCreated(t *testing.T) {
	db := newPendingTestDB(t)

	if _, err := execTaskCreated(db, taskCreatedUrgentEvent(t, 100, "42"), nil); err != nil {
		t.Fatalf("execTaskCreated: %v", err)
	}

	if got := pendingCount(t, db, 100); got != 1 {
		t.Errorf("pending rows: got %d, want 1", got)
	}
	if got := pendingAddedEventCount(t, db, 100); got != 1 {
		t.Errorf("pending.added events: got %d, want 1", got)
	}

	var reason string
	if err := db.QueryRow(
		`SELECT reason FROM project_next_pending WHERE task_id = ?`,
		taskIDString(100),
	).Scan(&reason); err != nil {
		t.Fatalf("read reason: %v", err)
	}
	if reason != autoAddUrgentPendingReason {
		t.Errorf("reason: got %q, want %q", reason, autoAddUrgentPendingReason)
	}
}

// Scenario 2: task.fields_updated setting phase=urgent on a previously
// non-urgent task inserts one pending row and emits one event.
func TestAutoAddUrgentPending_PhaseChanged(t *testing.T) {
	db := newPendingTestDB(t)

	if _, err := execTaskCreated(db, taskCreatedNonUrgentEvent(t, 200, "42"), nil); err != nil {
		t.Fatalf("execTaskCreated: %v", err)
	}
	if got := pendingCount(t, db, 200); got != 0 {
		t.Fatalf("precondition: pending after non-urgent create = %d, want 0", got)
	}

	if _, err := execTaskFieldsUpdated(db, taskFieldsUpdatedPhaseEvent(t, 200, "urgent", "42"), nil); err != nil {
		t.Fatalf("execTaskFieldsUpdated: %v", err)
	}

	if got := pendingCount(t, db, 200); got != 1 {
		t.Errorf("pending rows: got %d, want 1", got)
	}
	if got := pendingAddedEventCount(t, db, 200); got != 1 {
		t.Errorf("pending.added events: got %d, want 1", got)
	}
}

// Scenario 3: urgent → next → urgent → exactly one pending row (UNIQUE
// constraint enforces idempotency). Per Option A, the second urgent flip
// emits no event because the INSERT was a no-op.
func TestAutoAddUrgentPending_Idempotent(t *testing.T) {
	db := newPendingTestDB(t)

	if _, err := execTaskCreated(db, taskCreatedUrgentEvent(t, 300, "42"), nil); err != nil {
		t.Fatalf("execTaskCreated urgent: %v", err)
	}
	if _, err := execTaskFieldsUpdated(db, taskFieldsUpdatedPhaseEvent(t, 300, "next", "42"), nil); err != nil {
		t.Fatalf("execTaskFieldsUpdated -> next: %v", err)
	}
	if _, err := execTaskFieldsUpdated(db, taskFieldsUpdatedPhaseEvent(t, 300, "urgent", "42"), nil); err != nil {
		t.Fatalf("execTaskFieldsUpdated -> urgent: %v", err)
	}

	if got := pendingCount(t, db, 300); got != 1 {
		t.Errorf("pending rows after urgent→next→urgent: got %d, want 1", got)
	}
	if got := pendingAddedEventCount(t, db, 300); got != 1 {
		t.Errorf("pending.added events: got %d, want 1 (Option A: suppress on no-op insert)", got)
	}
}

// Scenario 4: a phase change to a non-urgent value (next → now) leaves
// project_next_pending untouched and emits no pending.added event.
func TestAutoAddUrgentPending_NonUrgentPhaseChange(t *testing.T) {
	db := newPendingTestDB(t)

	if _, err := execTaskCreated(db, taskCreatedNonUrgentEvent(t, 400, "42"), nil); err != nil {
		t.Fatalf("execTaskCreated: %v", err)
	}
	if _, err := execTaskFieldsUpdated(db, taskFieldsUpdatedPhaseEvent(t, 400, "later", "42"), nil); err != nil {
		t.Fatalf("execTaskFieldsUpdated: %v", err)
	}

	if got := pendingCount(t, db, 400); got != 0 {
		t.Errorf("pending rows: got %d, want 0", got)
	}
	if got := pendingAddedEventCount(t, db, 400); got != 0 {
		t.Errorf("pending.added events: got %d, want 0", got)
	}
}

// Scenario 5: the project_next row is created on demand when no curated
// list exists yet (the pending FK requires a parent row).
func TestAutoAddUrgentPending_CreatesProjectNextOnDemand(t *testing.T) {
	db := newPendingTestDB(t)

	var preCount int
	db.QueryRow("SELECT COUNT(*) FROM project_next").Scan(&preCount)
	if preCount != 0 {
		t.Fatalf("precondition: project_next rows = %d, want 0", preCount)
	}

	if _, err := execTaskCreated(db, taskCreatedUrgentEvent(t, 500, "42"), nil); err != nil {
		t.Fatalf("execTaskCreated: %v", err)
	}

	var postCount int
	db.QueryRow("SELECT COUNT(*) FROM project_next").Scan(&postCount)
	if postCount != 1 {
		t.Errorf("project_next rows after auto-add: got %d, want 1", postCount)
	}
	if got := pendingCount(t, db, 500); got != 1 {
		t.Errorf("pending rows: got %d, want 1", got)
	}
}

// A second task in the same project reuses the existing project_next row
// rather than creating a duplicate (UNIQUE on project_id would error
// otherwise).
func TestAutoAddUrgentPending_ReusesExistingProjectNext(t *testing.T) {
	db := newPendingTestDB(t)

	if _, err := execTaskCreated(db, taskCreatedUrgentEvent(t, 600, "42"), nil); err != nil {
		t.Fatalf("first urgent create: %v", err)
	}
	if _, err := execTaskCreated(db, taskCreatedUrgentEvent(t, 601, "42"), nil); err != nil {
		t.Fatalf("second urgent create: %v", err)
	}

	var headers int
	db.QueryRow("SELECT COUNT(*) FROM project_next").Scan(&headers)
	if headers != 1 {
		t.Errorf("project_next rows: got %d, want 1", headers)
	}

	var pendingRows int
	db.QueryRow(
		`SELECT COUNT(*) FROM project_next_pending
		 WHERE task_id IN (?, ?)`,
		taskIDString(600), taskIDString(601),
	).Scan(&pendingRows)
	if pendingRows != 2 {
		t.Errorf("pending rows: got %d, want 2", pendingRows)
	}
}

// When the event carries no session_id (e.g. system-emitted task.created),
// the auto-add side effect is skipped entirely — project_next_events.session_id
// is NOT NULL and we'd rather degrade than violate the FK or write an
// audit row with bogus attribution.
func TestAutoAddUrgentPending_SkipsWithoutSessionID(t *testing.T) {
	db := newPendingTestDB(t)

	evt := taskCreatedUrgentEvent(t, 700, "")
	if _, err := execTaskCreated(db, evt, nil); err != nil {
		t.Fatalf("execTaskCreated: %v", err)
	}

	if got := pendingCount(t, db, 700); got != 0 {
		t.Errorf("pending rows: got %d, want 0", got)
	}
	if got := pendingAddedEventCount(t, db, 700); got != 0 {
		t.Errorf("pending.added events: got %d, want 0", got)
	}
}

// Through the internal dispatch() function (used by every entry point —
// Execute, PreAllocateTaskID's execAndCommit, BeginImmediate's
// execAndCommit). Confirms task.created routes via the dispatcher into
// execTaskCreated and then triggers autoAddUrgentPending in the same
// invocation, end-to-end with no manual call into the side-effect helper.
func TestDispatch_TaskCreatedUrgent_AutoAddsPending(t *testing.T) {
	db := newPendingTestDB(t)

	evt := taskCreatedUrgentEvent(t, 900, "42")
	res, err := dispatch(db, evt, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.TaskID != 900 {
		t.Errorf("result.TaskID = %d, want 900", res.TaskID)
	}

	if got := pendingCount(t, db, 900); got != 1 {
		t.Errorf("pending rows: got %d, want 1", got)
	}
	if got := pendingAddedEventCount(t, db, 900); got != 1 {
		t.Errorf("pending.added events: got %d, want 1", got)
	}
}

// The pending.added payload includes the originating trigger kind so a
// reader of project_next_events can distinguish auto-add at creation
// from auto-add via a phase flip.
func TestAutoAddUrgentPending_PayloadIncludesTrigger(t *testing.T) {
	db := newPendingTestDB(t)

	if _, err := execTaskCreated(db, taskCreatedUrgentEvent(t, 800, "42"), nil); err != nil {
		t.Fatalf("urgent create: %v", err)
	}
	if _, err := execTaskCreated(db, taskCreatedNonUrgentEvent(t, 801, "42"), nil); err != nil {
		t.Fatalf("non-urgent create: %v", err)
	}
	if _, err := execTaskFieldsUpdated(db, taskFieldsUpdatedPhaseEvent(t, 801, "urgent", "42"), nil); err != nil {
		t.Fatalf("phase flip: %v", err)
	}

	rows, err := db.Query(
		`SELECT payload FROM project_next_events WHERE kind = 'pending.added'
		 ORDER BY id`,
	)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	var triggers []string
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		trig, _ := p["trigger"].(string)
		triggers = append(triggers, trig)
	}
	if len(triggers) != 2 {
		t.Fatalf("event count: got %d, want 2", len(triggers))
	}
	if triggers[0] != string(KindTaskCreated) {
		t.Errorf("event[0] trigger: got %q, want %q", triggers[0], KindTaskCreated)
	}
	if triggers[1] != string(KindTaskFieldsUpdated) {
		t.Errorf("event[1] trigger: got %q, want %q", triggers[1], KindTaskFieldsUpdated)
	}
}
