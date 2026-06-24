// Black-box tests for the maybe-parent invariant (ED-1510): a task may not
// be both phase=maybe and parented. Enforced in the Go executor across the
// three write paths — task.created, task.fields_updated, task.moved — as the
// single-writer half of the defense-in-depth gate (the Python CLI is the
// other half).
package events_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

const maybeParentErrFragment = "maybe-phase task cannot have a parent"

// TestExecute_TaskCreated_RejectsMaybeWithParent verifies a task.created
// event with phase=maybe and a parent is rejected.
func TestExecute_TaskCreated_RejectsMaybeWithParent(t *testing.T) {
	db := withExecutorDB(t)

	// A real parent must exist for the FK.
	parentEvt := newTaskCreatedEvent(t, 700, "parent")
	if _, err := events.Execute(parentEvt, nil); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	parentID := int64(700)
	payload, err := json.Marshal(events.TaskCreatedPayload{
		Title:    "maybe-child",
		Phase:    "maybe",
		Status:   "needs_plan",
		Type:     "task",
		ParentID: &parentID,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	evt := &events.Event{
		V:       events.Version,
		TS:      "5WYM00000700",
		Kind:    events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(701)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
	_, err = events.Execute(evt, nil)
	if err == nil {
		t.Fatal("Execute: want rejection, got nil error")
	}
	if !strings.Contains(err.Error(), maybeParentErrFragment) {
		t.Errorf("error = %q, want substring %q", err.Error(), maybeParentErrFragment)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM tasks WHERE id = ?", 701).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("rejected task was inserted (count=%d), want 0", count)
	}
}

// TestExecute_TaskCreated_AllowsMaybeRoot verifies phase=maybe with no parent
// is legal.
func TestExecute_TaskCreated_AllowsMaybeRoot(t *testing.T) {
	db := withExecutorDB(t)

	payload, _ := json.Marshal(events.TaskCreatedPayload{
		Title:  "maybe-root",
		Phase:  "maybe",
		Status: "needs_plan",
		Type:   "task",
	})
	evt := &events.Event{
		V:       events.Version,
		TS:      "5WYM00000702",
		Kind:    events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(702)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: want success, got %v", err)
	}
	var phase string
	if err := db.QueryRow("SELECT phase FROM tasks WHERE id = ?", 702).Scan(&phase); err != nil {
		t.Fatalf("query: %v", err)
	}
	if phase != "maybe" {
		t.Errorf("phase = %q, want maybe", phase)
	}
}

// TestExecute_TaskCreated_AllowsParentedNonMaybe verifies a parented task at
// a committed phase is legal.
func TestExecute_TaskCreated_AllowsParentedNonMaybe(t *testing.T) {
	withExecutorDB(t)

	if _, err := events.Execute(newTaskCreatedEvent(t, 703, "parent"), nil); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	parentID := int64(703)
	payload, _ := json.Marshal(events.TaskCreatedPayload{
		Title:    "now-child",
		Phase:    "now",
		Status:   "needs_plan",
		Type:     "task",
		ParentID: &parentID,
	})
	evt := &events.Event{
		V:       events.Version,
		TS:      "5WYM00000704",
		Kind:    events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(704)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: want success, got %v", err)
	}
}

// fieldsUpdatedEvent builds a task.fields_updated event for the given task.
func fieldsUpdatedEvent(t *testing.T, id int64, fields map[string]any) *events.Event {
	t.Helper()
	payload, err := json.Marshal(events.TaskFieldsUpdatedPayload{Fields: fields})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &events.Event{
		V:       events.Version,
		TS:      "5WYM00000710",
		Kind:    events.KindTaskFieldsUpdated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(id)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
}

// TestExecute_FieldsUpdated_RejectsReparentMaybe verifies reparenting an
// existing maybe-phase task under a parent is rejected (phase from DB).
func TestExecute_FieldsUpdated_RejectsReparentMaybe(t *testing.T) {
	withExecutorDB(t)

	if _, err := events.Execute(newTaskCreatedEvent(t, 710, "parent"), nil); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	// Create a parentless maybe task.
	maybePayload, _ := json.Marshal(events.TaskCreatedPayload{
		Title: "maybe-task", Phase: "maybe", Status: "needs_plan", Type: "task",
	})
	maybeEvt := &events.Event{
		V: events.Version, TS: "5WYM00000711", Kind: events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(711)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: maybePayload,
	}
	if _, err := events.Execute(maybeEvt, nil); err != nil {
		t.Fatalf("seed maybe task: %v", err)
	}

	evt := fieldsUpdatedEvent(t, 711, map[string]any{"parent_id": float64(710)})
	_, err := events.Execute(evt, nil)
	if err == nil || !strings.Contains(err.Error(), maybeParentErrFragment) {
		t.Fatalf("Execute: want rejection with %q, got %v", maybeParentErrFragment, err)
	}
}

// TestExecute_FieldsUpdated_RejectsPhaseMaybeOnChild verifies setting
// phase=maybe on a parented task is rejected (parent from DB).
func TestExecute_FieldsUpdated_RejectsPhaseMaybeOnChild(t *testing.T) {
	withExecutorDB(t)

	if _, err := events.Execute(newTaskCreatedEvent(t, 720, "parent"), nil); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	parentID := int64(720)
	childPayload, _ := json.Marshal(events.TaskCreatedPayload{
		Title: "child", Phase: "now", Status: "needs_plan", Type: "task",
		ParentID: &parentID,
	})
	childEvt := &events.Event{
		V: events.Version, TS: "5WYM00000721", Kind: events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(721)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: childPayload,
	}
	if _, err := events.Execute(childEvt, nil); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	evt := fieldsUpdatedEvent(t, 721, map[string]any{"phase": "maybe"})
	_, err := events.Execute(evt, nil)
	if err == nil || !strings.Contains(err.Error(), maybeParentErrFragment) {
		t.Fatalf("Execute: want rejection with %q, got %v", maybeParentErrFragment, err)
	}
}

// TestExecute_FieldsUpdated_AllowsAtomicPromoteAndParent verifies the atomic
// combo (phase=next + parent in one update) is legal.
func TestExecute_FieldsUpdated_AllowsAtomicPromoteAndParent(t *testing.T) {
	db := withExecutorDB(t)

	if _, err := events.Execute(newTaskCreatedEvent(t, 730, "parent"), nil); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	maybePayload, _ := json.Marshal(events.TaskCreatedPayload{
		Title: "maybe-task", Phase: "maybe", Status: "needs_plan", Type: "task",
	})
	maybeEvt := &events.Event{
		V: events.Version, TS: "5WYM00000731", Kind: events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(731)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: maybePayload,
	}
	if _, err := events.Execute(maybeEvt, nil); err != nil {
		t.Fatalf("seed maybe task: %v", err)
	}

	evt := fieldsUpdatedEvent(t, 731, map[string]any{
		"phase": "next", "parent_id": float64(730),
	})
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: want success for atomic promote+parent, got %v", err)
	}
	var phase string
	var parent int64
	if err := db.QueryRow("SELECT phase, parent_id FROM tasks WHERE id = ?", 731).
		Scan(&phase, &parent); err != nil {
		t.Fatalf("query: %v", err)
	}
	if phase != "next" || parent != 730 {
		t.Errorf("got phase=%q parent=%d, want next/730", phase, parent)
	}
}

// TestExecute_FieldsUpdated_AllowsUnrelatedEditOnViolatingRow verifies an
// edit that touches neither phase nor parent_id is NOT blocked, even when the
// row already violates the invariant (pre-existing data must stay editable).
func TestExecute_FieldsUpdated_AllowsUnrelatedEditOnViolatingRow(t *testing.T) {
	db := withExecutorDB(t)

	if _, err := events.Execute(newTaskCreatedEvent(t, 740, "parent"), nil); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	// Force a pre-existing violation directly in the DB (bypassing the gate).
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, status, parent_id, created_at, updated_at)
		 VALUES (741, 1, 'maybe', 'legacy-violation', 'needs_plan', 740, '2026-05-30T00:00:00', '2026-05-30T00:00:00')`,
	); err != nil {
		t.Fatalf("seed violating row: %v", err)
	}

	evt := fieldsUpdatedEvent(t, 741, map[string]any{"description": "edited"})
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: want success for unrelated edit, got %v", err)
	}
}

// TestExecute_TaskMoved_RejectsMaybeUnderParent verifies moving a maybe-phase
// task under a parent is rejected.
func TestExecute_TaskMoved_RejectsMaybeUnderParent(t *testing.T) {
	withExecutorDB(t)

	if _, err := events.Execute(newTaskCreatedEvent(t, 750, "parent"), nil); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	maybePayload, _ := json.Marshal(events.TaskCreatedPayload{
		Title: "maybe-task", Phase: "maybe", Status: "needs_plan", Type: "task",
	})
	maybeEvt := &events.Event{
		V: events.Version, TS: "5WYM00000751", Kind: events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(751)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: maybePayload,
	}
	if _, err := events.Execute(maybeEvt, nil); err != nil {
		t.Fatalf("seed maybe task: %v", err)
	}

	newParent := int64(750)
	movePayload, _ := json.Marshal(events.TaskMovedPayload{NewParentID: &newParent})
	moveEvt := &events.Event{
		V: events.Version, TS: "5WYM00000752", Kind: events.KindTaskMoved,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(751)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: movePayload,
	}
	_, err := events.Execute(moveEvt, nil)
	if err == nil || !strings.Contains(err.Error(), maybeParentErrFragment) {
		t.Fatalf("Execute: want rejection with %q, got %v", maybeParentErrFragment, err)
	}
}

// TestExecute_TaskMoved_AllowsMaybeToRoot verifies moving a maybe-phase task
// to root (NewParentID nil) is legal.
func TestExecute_TaskMoved_AllowsMaybeToRoot(t *testing.T) {
	db := withExecutorDB(t)

	// A parented maybe row (pre-existing violation) being moved to root.
	if _, err := events.Execute(newTaskCreatedEvent(t, 760, "parent"), nil); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, status, parent_id, created_at, updated_at)
		 VALUES (761, 1, 'maybe', 'legacy-violation', 'needs_plan', 760, '2026-05-30T00:00:00', '2026-05-30T00:00:00')`,
	); err != nil {
		t.Fatalf("seed violating row: %v", err)
	}

	movePayload, _ := json.Marshal(events.TaskMovedPayload{NewParentID: nil})
	moveEvt := &events.Event{
		V: events.Version, TS: "5WYM00000762", Kind: events.KindTaskMoved,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: itoaInt64(761)},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: movePayload,
	}
	if _, err := events.Execute(moveEvt, nil); err != nil {
		t.Fatalf("Execute: want success moving maybe to root, got %v", err)
	}
	var parent any
	if err := db.QueryRow("SELECT parent_id FROM tasks WHERE id = ?", 761).Scan(&parent); err != nil {
		t.Fatalf("query: %v", err)
	}
	if parent != nil {
		t.Errorf("parent_id = %v, want NULL after move to root", parent)
	}
}
