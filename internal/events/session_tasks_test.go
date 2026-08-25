package events

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
	_ "modernc.org/sqlite"
)

// newSessionTasksTestDB opens a file-backed SQLite DB in t.TempDir() and
// applies the V0 baseline schema plus the session_tasks table (V9 +
// V11 retrofit). Seeds one project so task inserts satisfy their
// project_id FK. The connection is closed automatically on test cleanup.
func newSessionTasksTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	// Route ConfigDir() (and thus the machine-local diagnostic log written by
	// the claim/release executors) to an isolated temp dir so tests never append
	// to the developer's real ~/.config/endless log.
	t.Cleanup(monitor.SetTestDB(db))
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_tasks (
		id         INTEGER PRIMARY KEY,
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
		Status: "unplanned",
		Type:   "todo",
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

// sessionTaskRelation returns the relation slug recorded for a (session, task)
// pair by joining session_tasks.relation_id to its mirror table. A NULL
// relation_id (no classification) yields the empty string.
func sessionTaskRelation(t *testing.T, db *sql.DB, sessionID, taskID int64) string {
	t.Helper()
	var slug sql.NullString
	err := db.QueryRow(
		`SELECT r.slug FROM session_tasks st
		 LEFT JOIN session_task_relations r ON r.id = st.relation_id
		 WHERE st.session_id = ? AND st.task_id = ?`,
		sessionID, taskID,
	).Scan(&slug)
	if err == sql.ErrNoRows {
		t.Fatalf("no session_tasks row for (%d, %d)", sessionID, taskID)
	}
	if err != nil {
		t.Fatalf("query relation: %v", err)
	}
	return slug.String
}

func taskClaimedEvent(t *testing.T, taskID int64, actor Actor) *Event {
	t.Helper()
	sessionID, err := strconv.ParseInt(actor.SessionID, 10, 64)
	if err != nil {
		t.Fatalf("parse session id %q: %v", actor.SessionID, err)
	}
	payload, err := json.Marshal(TaskClaimedPayload{SessionID: sessionID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-05-16T00:00:03",
		Kind:    KindTaskClaimed,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Actor:   actor,
		Payload: payload,
	}
}

// TestSessionTasks_RelationClassification verifies that the relation_id column
// is set from the triggering event kind at capture time (E-1462): task.created
// → surfaced, task.claimed → claimed, an incidental task.fields_updated → revisited.
func TestSessionTasks_RelationClassification(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "surfaced" {
		t.Errorf("created task: relation = %q, want surfaced", got)
	}

	if _, err := dispatch(db, taskClaimedEvent(t, 101, actor), nil); err != nil {
		t.Fatalf("dispatch claim: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 101); got != "claimed" {
		t.Errorf("claimed task: relation = %q, want claimed", got)
	}

	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 102, actor), nil); err != nil {
		t.Fatalf("dispatch update: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 102); got != "revisited" {
		t.Errorf("incidentally edited task: relation = %q, want revisited", got)
	}
}

// TestSessionTasks_RelationNeverDowngrades is the half of E-1462's set-once rule
// that E-1696's upgrade-only ladder preserves: a weaker later capture must not
// weaken a stronger stored relation. A task surfaced (created) in-session that is
// later edited stays surfaced; it does not downgrade to revisited.
func TestSessionTasks_RelationNeverDowngrades(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch update: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "surfaced" {
		t.Errorf("downgraded: relation = %q after later edit, want surfaced", got)
	}
}

// TestSessionTasks_RelationUpgrades is the half E-1696 ADDS: a stronger later
// capture replaces a weaker stored relation. This is the bug set-once had.
//
// The load-bearing case is referenced→claimed. `endless guide`'s happy path is
// `task show <id>` THEN `task claim <id>`, so under set-once the read gate would
// pin every session's own claimed task at `referenced` forever — the read arrives
// first and the claim could never correct it. surfaced→claimed is the same defect
// set-once already shipped with: a task filed and then claimed in one session
// read `surfaced`, not `claimed`.
//
// upsertSessionTask is called directly for the `referenced` rows because no
// emitter produces that relation yet (the read gate ships with the machine-user
// ledger, E-1673); every other step goes through a real event.
func TestSessionTasks_RelationUpgrades(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	// referenced → claimed (the read-before-claim happy path).
	if err := upsertSessionTask(db, "42", 100, sessiontaskrelation.RelationReferenced); err != nil {
		t.Fatalf("seed referenced: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "referenced" {
		t.Fatalf("seed: relation = %q, want referenced", got)
	}
	if _, err := dispatch(db, taskClaimedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch claim: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "claimed" {
		t.Errorf("read-then-claim: relation = %q, want claimed", got)
	}

	// referenced → revisited (a read the session later acts on).
	if err := upsertSessionTask(db, "42", 101, sessiontaskrelation.RelationReferenced); err != nil {
		t.Fatalf("seed referenced: %v", err)
	}
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 101, actor), nil); err != nil {
		t.Fatalf("dispatch update: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 101); got != "revisited" {
		t.Errorf("read-then-edit: relation = %q, want revisited", got)
	}

	// surfaced → claimed (file it, then claim it).
	if _, err := dispatch(db, taskCreatedEvent(t, 102, actor), nil); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if _, err := dispatch(db, taskClaimedEvent(t, 102, actor), nil); err != nil {
		t.Fatalf("dispatch claim: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 102); got != "claimed" {
		t.Errorf("create-then-claim: relation = %q, want claimed", got)
	}
}

// TestSessionTasks_RelationEqualCaptureIsNoOp pins that an equal relation does
// not count as an upgrade — Outranks is strict. It matters because every repeat
// touch of the same kind takes this path, and a non-strict comparison would
// rewrite relation_id on every one of them for no reason.
func TestSessionTasks_RelationEqualCaptureIsNoOp(t *testing.T) {
	db := newSessionTasksTestDB(t)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch update: %v", err)
	}
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch second update: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "revisited" {
		t.Errorf("repeat capture: relation = %q, want revisited", got)
	}
}

// TestSessionTasks_RelationFillsNullHistoricalRow covers the pre-E-1462 rows the
// schema comment promises to heal: relation_id NULL ranks below every real
// relation, so the next capture — even the weakest one — fills it in rather than
// leaving it NULL forever. relationRankCase's ELSE arm is what makes this work,
// and a CASE that mishandled NULL would silently strand these rows.
func TestSessionTasks_RelationFillsNullHistoricalRow(t *testing.T) {
	db := newSessionTasksTestDB(t)

	if _, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
		 VALUES (42, 100, NULL, '2026-01-01T00:00:00', '2026-01-01T00:00:00')`,
	); err != nil {
		t.Fatalf("seed historical row: %v", err)
	}
	if err := upsertSessionTask(db, "42", 100, sessiontaskrelation.RelationReferenced); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "referenced" {
		t.Errorf("NULL historical row: relation = %q, want referenced", got)
	}
}

// TestSessionTasks_CreatedBySession verifies that a task.created event
// from an ActorSession produces exactly one session_tasks row with
// created_at == updated_at.
func TestSessionTasks_CreatedBySession(t *testing.T) {
	db := newSessionTasksTestDB(t)

	evt := taskCreatedEvent(t, 100, Actor{Kind: ActorSession, ID: "s1", SessionID: "42"})
	if _, err := dispatch(db, evt, nil); err != nil {
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
	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor), nil); err != nil {
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

	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor), nil); err != nil {
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

// TestSessionTasks_CLIActorWithSessionID verifies that a CLI actor
// carrying a session_id DOES produce a row. This is the path the
// user-facing endless CLI takes — Python's emit_event defaults
// actor_kind="cli" but populates session_id via the resolver. Per
// event.go's Actor docstring, that combination means "the user ran a
// CLI command from inside a Claude session"; the touch IS session-
// attributable. (Regression guard for the bug where shouldRecordSession
// Touch's strict Kind==ActorSession check rejected every real touch.)
func TestSessionTasks_CLIActorWithSessionID(t *testing.T) {
	db := newSessionTasksTestDB(t)

	evt := taskCreatedEvent(t, 100, Actor{Kind: ActorCLI, ID: "user@host", SessionID: "42"})
	if _, err := dispatch(db, evt, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if n := countSessionTasks(t, db); n != 1 {
		t.Errorf("expected 1 session_tasks row for ActorCLI with SessionID set, got %d", n)
	}
	if _, _, ok := sessionTasksRow(t, db, 42, 100); !ok {
		t.Error("expected (42, 100) row to exist from cli+session_id event")
	}
}

// TestSessionTasks_NoRowForCLIActorWithoutSessionID verifies the
// session_id presence requirement: a CLI actor with no session_id does
// NOT produce a row (no session to attribute the touch to).
func TestSessionTasks_NoRowForCLIActorWithoutSessionID(t *testing.T) {
	db := newSessionTasksTestDB(t)

	evt := taskCreatedEvent(t, 100, Actor{Kind: ActorCLI, ID: "user@host", SessionID: ""})
	if _, err := dispatch(db, evt, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if n := countSessionTasks(t, db); n != 0 {
		t.Errorf("expected 0 session_tasks rows for ActorCLI with no SessionID, got %d", n)
	}
}

// TestSessionTasks_NoRowForEmptySessionID verifies the empty-string guard
// using the realistic production shape: a CLI emit from outside any tmux
// session resolves no session_id, so the actor arrives as ActorCLI with
// SessionID="" and must NOT produce a row.
func TestSessionTasks_NoRowForEmptySessionID(t *testing.T) {
	db := newSessionTasksTestDB(t)

	evt := taskCreatedEvent(t, 100, Actor{Kind: ActorCLI, ID: "user@host", SessionID: ""})
	if _, err := dispatch(db, evt, nil); err != nil {
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

	if _, err := dispatch(db, taskCreatedEvent(t, 100, Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}), nil); err != nil {
		t.Fatalf("dispatch s1: %v", err)
	}
	// Different session, same task — uses task.fields_updated since
	// task.created on an existing id would fail the INSERT.
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, Actor{Kind: ActorSession, ID: "s2", SessionID: "43"}), nil); err != nil {
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
	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch task 100: %v", err)
	}
	if _, err := dispatch(db, taskCreatedEvent(t, 101, actor), nil); err != nil {
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
	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch update 1: %v", err)
	}
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch update 2: %v", err)
	}

	if n := countSessionTasks(t, db); n != 1 {
		t.Errorf("expected 1 session_tasks row after replay, got %d", n)
	}
}

// TestSessionTasks_DeletedTaskRetainsRow verifies the no-FK design: a
// task.deleted records a session_tasks row, and the row survives the removal.
// If session_tasks had a FK on task_id with ON DELETE CASCADE, this test would
// fail.
//
// E-1929 changed what "the task is gone" means: removal marks the row
// removed = 1 rather than DELETEing it, so the assertion below is that the row
// is RETAINED and marked, and that it is invisible through live_tasks. That
// retention is what makes the surviving session_tasks row safe — its task id can
// never be re-minted, so the row can never reattach to unrelated work.
func TestSessionTasks_DeletedTaskRetainsRow(t *testing.T) {
	db := newSessionTasksTestDB(t)

	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}
	if _, err := dispatch(db, taskCreatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if _, err := dispatch(db, taskDeletedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch delete: %v", err)
	}

	if n := countSessionTasks(t, db); n != 1 {
		t.Errorf("expected session_tasks row to survive task deletion, got count=%d", n)
	}
	if _, _, ok := sessionTasksRow(t, db, 42, 100); !ok {
		t.Error("expected (42, 100) row to remain after task deletion")
	}

	// And confirm the task row is retained, flagged, and hidden from reads.
	var removed int
	if err := db.QueryRow("SELECT removed FROM tasks WHERE id = ?", 100).Scan(&removed); err != nil {
		t.Fatalf("read removed flag for task 100 (row should be retained): %v", err)
	}
	if removed != 1 {
		t.Errorf("tasks.removed = %d for removed task 100, want 1", removed)
	}
	var liveCount int
	if err := db.QueryRow("SELECT count(*) FROM live_tasks WHERE id = ?", 100).Scan(&liveCount); err != nil {
		t.Fatalf("count live_tasks: %v", err)
	}
	if liveCount != 0 {
		t.Errorf("removed task 100 still visible through live_tasks (%d row(s))", liveCount)
	}
}
