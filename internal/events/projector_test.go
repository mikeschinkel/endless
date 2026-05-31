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
		Status: "needs_plan",
		Type:   "task",
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
	if status != "needs_plan" {
		t.Errorf("status = %q, want needs_plan", status)
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
		Status: "needs_plan",
		Type:   "task",
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
