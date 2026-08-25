// Black-box tests for the parent-cycle invariant (E-2067): no write to
// tasks.parent_id may make a task its own ancestor. The ancestor walk used to
// live inside execTaskMoved only, so `task update --parent` — which writes the
// same column through task.fields_updated — could close a loop that every
// recursive CTE over the task tree then walks forever. The guard is now a
// shared validator called from both executors.
package events_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

const cycleErrFragment = "circular reference"

// mustMarshal JSON-encodes an event payload or fails the test.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// seedTaskTree creates tasks with the given parent links through the real
// task.created path, so every row exists exactly as the executor would make it.
// A parent of 0 means root. Parents must be created before their children.
func seedTaskTree(t *testing.T, ids [][2]int64) {
	t.Helper()
	for _, pair := range ids {
		id, parent := pair[0], pair[1]
		evt := newTaskCreatedEvent(t, id, "task")
		if parent != 0 {
			p := parent
			evt = withParent(t, evt, &p)
		}
		if _, err := events.Execute(evt, nil); err != nil {
			t.Fatalf("seed task %d (parent %d): %v", id, parent, err)
		}
	}
}

// withParent re-marshals a task.created event with a parent attached.
func withParent(t *testing.T, evt *events.Event, parent *int64) *events.Event {
	t.Helper()
	payload := events.TaskCreatedPayload{
		Title: "task", Phase: "now", Status: "unplanned", Type: "todo",
		ParentID: parent,
	}
	out := *evt
	out.Payload = mustMarshal(t, payload)
	return &out
}

// parentOf reads tasks.parent_id, returning 0 for a root task.
func parentOf(t *testing.T, db *sql.DB, id int64) int64 {
	t.Helper()
	var parent sql.NullInt64
	if err := db.QueryRow("SELECT parent_id FROM tasks WHERE id = ?", id).Scan(&parent); err != nil {
		t.Fatalf("read parent of %d: %v", id, err)
	}
	if !parent.Valid {
		return 0
	}
	return parent.Int64
}

// moveEvent builds a task.moved event for the given task; parent 0 means root.
func moveEvent(t *testing.T, id, parent int64) *events.Event {
	t.Helper()
	var newParent *int64
	if parent != 0 {
		newParent = &parent
	}
	return &events.Event{
		V: events.Version, TS: "5WYM00000900", Kind: events.KindTaskMoved,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(id)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: mustMarshal(t, events.TaskMovedPayload{NewParentID: newParent}),
	}
}

// ── task.fields_updated: the hole this task closes ──────────────────────────

// TestExecute_FieldsUpdated_RejectsDirectParentCycle reproduces the filed
// defect: A is B's parent, then `update A --parent B` is applied. Before
// E-2067 both rows ended up pointing at each other.
func TestExecute_FieldsUpdated_RejectsDirectParentCycle(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{900, 0}, {901, 900}})

	evt := fieldsUpdatedEvent(t, 900, map[string]any{"parent_id": float64(901)})
	_, err := events.Execute(evt, nil)
	if err == nil || !strings.Contains(err.Error(), cycleErrFragment) {
		t.Fatalf("Execute: want rejection containing %q, got %v", cycleErrFragment, err)
	}
	if got := parentOf(t, db, 900); got != 0 {
		t.Errorf("task 900 parent = %d, want 0 (unchanged)", got)
	}
	if got := parentOf(t, db, 901); got != 900 {
		t.Errorf("task 901 parent = %d, want 900 (unchanged)", got)
	}
}

// TestExecute_FieldsUpdated_RejectsSelfParent verifies a task cannot be made
// its own parent — the one-node cycle the original move walk missed, because
// it started climbing at the target parent rather than testing it.
func TestExecute_FieldsUpdated_RejectsSelfParent(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{910, 0}})

	evt := fieldsUpdatedEvent(t, 910, map[string]any{"parent_id": float64(910)})
	_, err := events.Execute(evt, nil)
	if err == nil || !strings.Contains(err.Error(), cycleErrFragment) {
		t.Fatalf("Execute: want rejection containing %q, got %v", cycleErrFragment, err)
	}
	if got := parentOf(t, db, 910); got != 0 {
		t.Errorf("task 910 parent = %d, want 0 (unchanged)", got)
	}
}

// TestExecute_FieldsUpdated_RejectsDeepParentCycle verifies the walk climbs
// past the immediate parent: 920 -> 921 -> 922, then `update 920 --parent 922`.
func TestExecute_FieldsUpdated_RejectsDeepParentCycle(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{920, 0}, {921, 920}, {922, 921}})

	evt := fieldsUpdatedEvent(t, 920, map[string]any{"parent_id": float64(922)})
	_, err := events.Execute(evt, nil)
	if err == nil || !strings.Contains(err.Error(), cycleErrFragment) {
		t.Fatalf("Execute: want rejection containing %q, got %v", cycleErrFragment, err)
	}
	if got := parentOf(t, db, 920); got != 0 {
		t.Errorf("task 920 parent = %d, want 0 (unchanged)", got)
	}
}

// TestExecute_FieldsUpdated_AllowsLegalReparent verifies the guard does not
// block an ordinary re-parent onto an unrelated subtree.
func TestExecute_FieldsUpdated_AllowsLegalReparent(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{930, 0}, {931, 930}, {932, 0}})

	evt := fieldsUpdatedEvent(t, 931, map[string]any{"parent_id": float64(932)})
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: want success for a legal re-parent, got %v", err)
	}
	if got := parentOf(t, db, 931); got != 932 {
		t.Errorf("task 931 parent = %d, want 932", got)
	}
}

// TestExecute_FieldsUpdated_AllowsClearingParentOutOfACycle verifies the
// documented escape hatch still works on a row that is already inside a cycle:
// `--parent 0` detaches, and detaching can never create one.
func TestExecute_FieldsUpdated_AllowsClearingParentOutOfACycle(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{940, 0}, {941, 940}})
	// Close the loop behind the executor's back — the state the live database
	// was actually found in.
	if _, err := db.Exec("UPDATE tasks SET parent_id = 941 WHERE id = 940"); err != nil {
		t.Fatalf("seed cycle: %v", err)
	}

	evt := fieldsUpdatedEvent(t, 940, map[string]any{"parent_id": nil})
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: want success clearing the parent, got %v", err)
	}
	if got := parentOf(t, db, 940); got != 0 {
		t.Errorf("task 940 parent = %d, want 0 after clearing", got)
	}
}

// TestExecute_FieldsUpdated_AllowsUnrelatedEditInsideACycle verifies an edit
// that does not touch parent_id is not blocked by a cycle the row is already
// in — pre-existing corruption must stay editable, exactly as the maybe-parent
// guard treats a pre-existing violation.
func TestExecute_FieldsUpdated_AllowsUnrelatedEditInsideACycle(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{950, 0}, {951, 950}})
	if _, err := db.Exec("UPDATE tasks SET parent_id = 951 WHERE id = 950"); err != nil {
		t.Fatalf("seed cycle: %v", err)
	}

	evt := fieldsUpdatedEvent(t, 950, map[string]any{"description": "edited"})
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: want success for an unrelated edit, got %v", err)
	}
	evt = fieldsUpdatedEvent(t, 950, map[string]any{"phase": "next"})
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: want success for a phase-only edit, got %v", err)
	}
}

// TestExecute_FieldsUpdated_TerminatesOnPreExistingCycle verifies a corrupt
// chain ABOVE the target parent is refused rather than walked forever. This
// test hanging is itself the failure mode being guarded against.
func TestExecute_FieldsUpdated_TerminatesOnPreExistingCycle(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{960, 0}, {961, 0}, {962, 961}})
	if _, err := db.Exec("UPDATE tasks SET parent_id = 962 WHERE id = 961"); err != nil {
		t.Fatalf("seed cycle: %v", err)
	}

	evt := fieldsUpdatedEvent(t, 960, map[string]any{"parent_id": float64(962)})
	_, err := events.Execute(evt, nil)
	if err == nil || !strings.Contains(err.Error(), cycleErrFragment) {
		t.Fatalf("Execute: want rejection containing %q, got %v", cycleErrFragment, err)
	}
	if got := parentOf(t, db, 960); got != 0 {
		t.Errorf("task 960 parent = %d, want 0 (unchanged)", got)
	}
}

// ── task.moved: the guard that already worked, and its own gap ──────────────

// TestExecute_TaskMoved_StillRejectsParentCycle verifies lifting the walk into
// the shared validator did not weaken the path it came from.
func TestExecute_TaskMoved_StillRejectsParentCycle(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{970, 0}, {971, 970}, {972, 971}})

	_, err := events.Execute(moveEvent(t, 970, 972), nil)
	if err == nil || !strings.Contains(err.Error(), cycleErrFragment) {
		t.Fatalf("Execute: want rejection containing %q, got %v", cycleErrFragment, err)
	}
	if got := parentOf(t, db, 970); got != 0 {
		t.Errorf("task 970 parent = %d, want 0 (unchanged)", got)
	}
}

// TestExecute_TaskMoved_RejectsSelfParent verifies the move path now refuses a
// self-parent too. The old inline walk started at the target parent and read
// ITS parent, so a root task moved under itself passed straight through.
func TestExecute_TaskMoved_RejectsSelfParent(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{980, 0}})

	_, err := events.Execute(moveEvent(t, 980, 980), nil)
	if err == nil || !strings.Contains(err.Error(), cycleErrFragment) {
		t.Fatalf("Execute: want rejection containing %q, got %v", cycleErrFragment, err)
	}
	if got := parentOf(t, db, 980); got != 0 {
		t.Errorf("task 980 parent = %d, want 0 (unchanged)", got)
	}
}

// TestExecute_TaskMoved_AllowsMoveToRoot verifies detaching still works.
func TestExecute_TaskMoved_AllowsMoveToRoot(t *testing.T) {
	db := withExecutorDB(t)
	seedTaskTree(t, [][2]int64{{990, 0}, {991, 990}})

	if _, err := events.Execute(moveEvent(t, 991, 0), nil); err != nil {
		t.Fatalf("Execute: want success moving to root, got %v", err)
	}
	if got := parentOf(t, db, 991); got != 0 {
		t.Errorf("task 991 parent = %d, want 0", got)
	}
}

// ── the validator itself ────────────────────────────────────────────────────

// TestValidateNoParentCycle_NilParentIsAlwaysLegal pins the contract the two
// executors rely on for their root/clear-parent paths.
func TestValidateNoParentCycle_NilParentIsAlwaysLegal(t *testing.T) {
	db := withExecutorDB(t)
	if err := events.ValidateNoParentCycle(db, 1, nil); err != nil {
		t.Fatalf("ValidateNoParentCycle(nil parent) = %v, want nil", err)
	}
}

// TestValidateNoParentCycle_UnknownParentIsNotACycle verifies an unreadable
// ancestor ends the walk quietly — referential integrity is the FK's job.
func TestValidateNoParentCycle_UnknownParentIsNotACycle(t *testing.T) {
	db := withExecutorDB(t)
	missing := int64(999999)
	if err := events.ValidateNoParentCycle(db, 1, &missing); err != nil {
		t.Fatalf("ValidateNoParentCycle(missing parent) = %v, want nil", err)
	}
}
