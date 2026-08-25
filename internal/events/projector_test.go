// Black-box tests for ProjectToTempDB. The projector reads a project's
// ledger segments and replays the events into a fresh temporary SQLite DB.
// Tests stage synthetic ledger files under <projectRoot>/.endless/db-ledger/
// using Writer.Append, then assert on the projection output.
package events_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
)

// TestProjectToTempDB_NoEventsReturnsError verifies that an empty ledger
// directory triggers the projector's "no events" early return. The
// projector treats an empty event set as a misuse (nothing to project),
// not a valid empty projection.
func TestProjectToTempDB_NoEventsReturnsError(t *testing.T) {
	dir := t.TempDir()

	_, _, err := events.ProjectToTempDB(dir)
	if err == nil {
		t.Fatalf("ProjectToTempDB on empty project root should error")
	}
}

// TestProjectToTempDB_TaskCreatedProducesRow verifies the projector's
// happy path: a single task.created event in a segment file replays into
// the temp DB as a tasks row with the payload's values.
func TestProjectToTempDB_TaskCreatedProducesRow(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "abcd")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	createdPayload, err := json.Marshal(events.TaskCreatedPayload{
		Title:  "Projector target",
		Phase:  "now",
		Status: "unplanned",
		Type:   "todo",
	})
	if err != nil {
		t.Fatalf("marshal created payload: %v", err)
	}
	createdEvt := events.Event{
		V:       events.Version,
		TS:      "5WYM00000001",
		Kind:    events.KindTaskCreated,
		Project: "proj-projector",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "777"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: createdPayload,
	}
	createdLine, err := json.Marshal(createdEvt)
	if err != nil {
		t.Fatalf("marshal created evt: %v", err)
	}
	if err := w.Append(createdLine); err != nil {
		t.Fatalf("Append created: %v", err)
	}

	tempPath, result, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { os.Remove(tempPath) })

	if result.EventsReplayed != 1 {
		t.Errorf("EventsReplayed = %d, want 1", result.EventsReplayed)
	}
	if result.TasksCreated != 1 {
		t.Errorf("TasksCreated = %d, want 1", result.TasksCreated)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", result.Errors)
	}

	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer db.Close()

	var (
		title  string
		status string
		phase  string
	)
	if err := db.QueryRow(
		"SELECT title, status, phase FROM tasks WHERE id = ?", 777,
	).Scan(&title, &status, &phase); err != nil {
		t.Fatalf("query projected task: %v", err)
	}
	if title != "Projector target" {
		t.Errorf("title = %q, want %q", title, "Projector target")
	}
	if status != "unplanned" {
		t.Errorf("status = %q, want unplanned", status)
	}
	if phase != "now" {
		t.Errorf("phase = %q, want now", phase)
	}
}

// TestProjectToTempDB_CreateThenUpdateApplied verifies that a sequence
// of task.created then task.fields_updated produces the final mutated
// state in the projected DB (i.e., events replay in order).
func TestProjectToTempDB_CreateThenUpdateApplied(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "1234")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	createdPayload, _ := json.Marshal(events.TaskCreatedPayload{
		Title:  "Original",
		Phase:  "now",
		Status: "unplanned",
		Type:   "todo",
	})
	createdEvt := events.Event{
		V:       events.Version,
		TS:      "5WYM00000001",
		Kind:    events.KindTaskCreated,
		Project: "proj-seq",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "808"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: createdPayload,
	}
	createdLine, _ := json.Marshal(createdEvt)
	if err := w.Append(createdLine); err != nil {
		t.Fatalf("Append created: %v", err)
	}

	updatePayload, _ := json.Marshal(events.TaskFieldsUpdatedPayload{
		Fields: map[string]any{"title": "Revised"},
	})
	updateEvt := events.Event{
		V:       events.Version,
		TS:      "5WYM00000002",
		Kind:    events.KindTaskFieldsUpdated,
		Project: "proj-seq",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "808"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: updatePayload,
	}
	updateLine, _ := json.Marshal(updateEvt)
	if err := w.Append(updateLine); err != nil {
		t.Fatalf("Append update: %v", err)
	}

	tempPath, result, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { os.Remove(tempPath) })

	if result.EventsReplayed != 2 {
		t.Errorf("EventsReplayed = %d, want 2", result.EventsReplayed)
	}
	if result.TasksUpdated != 1 {
		t.Errorf("TasksUpdated = %d, want 1", result.TasksUpdated)
	}

	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer db.Close()

	var title string
	if err := db.QueryRow("SELECT title FROM tasks WHERE id = ?", 808).Scan(&title); err != nil {
		t.Fatalf("query projected task: %v", err)
	}
	if title != "Revised" {
		t.Errorf("title after update = %q, want %q", title, "Revised")
	}
}

// appendTaskCreated stages one task.created event in dir's ledger.
func appendTaskCreated(t *testing.T, w *events.Writer, project, id, ts, title string) {
	t.Helper()
	payload, err := json.Marshal(events.TaskCreatedPayload{
		Title: title, Phase: "now", Status: "unplanned", Type: "todo",
	})
	if err != nil {
		t.Fatalf("marshal created payload: %v", err)
	}
	appendEvent(t, w, events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    events.KindTaskCreated,
		Project: project,
		Entity:  events.EntityRef{Type: events.EntityTask, ID: id},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	})
}

func appendEvent(t *testing.T, w *events.Writer, evt events.Event) {
	t.Helper()
	line, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal %s evt: %v", evt.Kind, err)
	}
	if err := w.Append(line); err != nil {
		t.Fatalf("Append %s: %v", evt.Kind, err)
	}
}

// allocatorFloor runs the exact query the task-id allocator uses
// (events.PreAllocateTaskID). It reads `tasks`, never `live_tasks` — that is
// the property under test.
func allocatorFloor(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var next int64
	if err := db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM tasks").Scan(&next); err != nil {
		t.Fatalf("allocator floor query: %v", err)
	}
	return next
}

// TestProjectToTempDB_TaskDeletedRetainsRowAndIDFloor is the executor/projector
// parity check for ED-1547 (E-1929), the failure that would otherwise ship
// looking complete.
//
// The executor marks a removed task `removed = 1`. If the projector still
// replayed task.deleted as a real DELETE, then rebuilding the DB from the ledger
// would drop the retained row, MAX(id) would fall back, and the next task
// allocated would REUSE the removed id — re-orphaning every FK-free row that
// deliberately outlives its task. Nothing in normal use would surface it.
//
// Deliberately NOT verified by rebuilding a real DB: `rebuild-db` is not yet
// reliable, so a test that used it would risk real data and produce failures
// attributable to the rebuild rather than to this change. A synthetic event
// stream into a temp DB exercises the same handler in isolation.
func TestProjectToTempDB_TaskDeletedRetainsRowAndIDFloor(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "dead")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	appendTaskCreated(t, w, "proj-removal", "900", "5WYM00000001", "Survivor")
	appendTaskCreated(t, w, "proj-removal", "901", "5WYM00000002", "Doomed")

	deletedPayload, err := json.Marshal(events.TaskDeletedPayload{Title: "Doomed"})
	if err != nil {
		t.Fatalf("marshal deleted payload: %v", err)
	}
	appendEvent(t, w, events.Event{
		V:       events.Version,
		TS:      "5WYM00000003",
		Kind:    events.KindTaskDeleted,
		Project: "proj-removal",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "901"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: deletedPayload,
	})

	tempPath, _, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { os.Remove(tempPath) })

	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer db.Close()

	var removed int
	if err := db.QueryRow("SELECT removed FROM tasks WHERE id = 901").Scan(&removed); err != nil {
		t.Fatalf("replayed removal deleted the row instead of marking it: %v", err)
	}
	if removed != 1 {
		t.Errorf("tasks.removed = %d for replayed removal, want 1", removed)
	}

	var live int
	if err := db.QueryRow("SELECT count(*) FROM live_tasks WHERE id = 901").Scan(&live); err != nil {
		t.Fatalf("count live_tasks: %v", err)
	}
	if live != 0 {
		t.Errorf("removed task visible through live_tasks (%d row(s))", live)
	}

	// The regression this whole change exists to prevent: the next id allocated
	// against the rebuilt DB must be ABOVE the removed one, never reuse it.
	if got := allocatorFloor(t, db); got != 902 {
		t.Errorf("allocator floor after replaying removal of the highest id = %d, want 902 "+
			"(a value of 901 means the id was re-freed and would be reused)", got)
	}
}

// TestProjectToTempDB_TaskBulkClearedRetainsRowsAndIDFloor is the same parity
// check for the bulk-clear path (`task import --replace`). Bulk clear retains
// too — one rule, no second orphaning path — so a rebuild must not re-free the
// ids it cleared either.
func TestProjectToTempDB_TaskBulkClearedRetainsRowsAndIDFloor(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "bulk")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	importedPayload, err := json.Marshal(events.TaskImportedPayload{
		Title: "From file", Phase: "now", Status: "unplanned", SourceFile: "PLAN.md",
	})
	if err != nil {
		t.Fatalf("marshal imported payload: %v", err)
	}
	for i, id := range []string{"910", "911"} {
		appendEvent(t, w, events.Event{
			V:       events.Version,
			TS:      []string{"5WYM00000001", "5WYM00000002"}[i],
			Kind:    events.KindTaskImported,
			Project: "proj-bulk",
			Entity:  events.EntityRef{Type: events.EntityTask, ID: id},
			Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
			Payload: importedPayload,
		})
	}

	clearedPayload, err := json.Marshal(events.TaskBulkClearedPayload{SourceFile: "PLAN.md"})
	if err != nil {
		t.Fatalf("marshal bulk_cleared payload: %v", err)
	}
	appendEvent(t, w, events.Event{
		V:       events.Version,
		TS:      "5WYM00000003",
		Kind:    events.KindTaskBulkCleared,
		Project: "proj-bulk",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "910"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: clearedPayload,
	})

	tempPath, _, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { os.Remove(tempPath) })

	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer db.Close()

	var retained int
	if err := db.QueryRow(
		"SELECT count(*) FROM tasks WHERE id IN (910, 911) AND removed = 1",
	).Scan(&retained); err != nil {
		t.Fatalf("count retained bulk-cleared rows: %v", err)
	}
	if retained != 2 {
		t.Errorf("bulk-cleared rows retained with removed = 1: %d, want 2", retained)
	}

	var live int
	if err := db.QueryRow("SELECT count(*) FROM live_tasks WHERE id IN (910, 911)").Scan(&live); err != nil {
		t.Fatalf("count live_tasks: %v", err)
	}
	if live != 0 {
		t.Errorf("bulk-cleared tasks visible through live_tasks (%d row(s))", live)
	}

	if got := allocatorFloor(t, db); got != 912 {
		t.Errorf("allocator floor after replaying bulk clear = %d, want 912 "+
			"(a lower value means the cleared ids were re-freed)", got)
	}
}

// TestProjectToTempDB_StatusChangeCarriesOutcome pins the round trip E-787
// asked for: a task declined with a reason must come back out of the ledger
// with that reason intact, because `outcome` is the only record of WHY a task
// was declined and it exists nowhere but the event.
//
// Asserted against the projection, which is where the claim lives. It used to
// be asserted by running `endless-go event rebuild-db --confirm` from
// tests/test_outcome.py and reading the outcome back out of the real database —
// a test that needed a built binary, wrote to a database, and made a projector
// claim through a copy-back that is refused since E-2062 and that never touched
// `outcome` on its own account.
func TestProjectToTempDB_StatusChangeCarriesOutcome(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "beef")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	const taskID = "909"
	appendTaskCreated(t, w, "proj-outcome", taskID, "5WYM00000001", "Sample")

	declinePayload, err := json.Marshal(events.TaskStatusChangedPayload{
		OldStatus: "unplanned",
		NewStatus: "declined",
		Outcome:   "round-trip reason",
	})
	if err != nil {
		t.Fatalf("marshal status payload: %v", err)
	}
	appendEvent(t, w, events.Event{
		V:       events.Version,
		TS:      "5WYM00000002",
		Kind:    events.KindTaskStatusChanged,
		Project: "proj-outcome",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: taskID},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: declinePayload,
	})

	tempPath, result, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { os.Remove(tempPath) })
	if len(result.Errors) > 0 {
		t.Errorf("projection reported errors: %v", result.Errors)
	}

	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer db.Close()

	var status, outcome string
	if err := db.QueryRow(
		"SELECT status, COALESCE(outcome,'') FROM tasks WHERE id = ?", 909,
	).Scan(&status, &outcome); err != nil {
		t.Fatalf("query projected task: %v", err)
	}
	if status != "declined" {
		t.Errorf("projected status = %q, want declined", status)
	}
	if outcome != "round-trip reason" {
		t.Errorf("projected outcome = %q, want %q", outcome, "round-trip reason")
	}
}
