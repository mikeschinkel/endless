package events_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

// TestProjectToTempDB_RetiredUntriagedLandsWhereE1993PutIt replays each shape
// of historical event that carries the retired `untriaged` status. The ledger
// is immutable, so a rebuild must land those tasks where migration 00008 put
// the live rows — `submitted` with a plan, `unplanned` without — or a rebuilt
// database would disagree with the one it rebuilds.
func TestProjectToTempDB_RetiredUntriagedLandsWhereE1993PutIt(t *testing.T) {
	dir := t.TempDir()
	w, err := events.NewWriter(dir, "e1993")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	const project = "proj-untriaged"
	ts := 0
	next := func() string {
		ts++
		return "5WYM0000" + string(rune('A'+ts/10)) + string(rune('0'+ts%10)) + "00"
	}
	emit := func(kind events.Kind, id string, payload any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		appendEvent(t, w, events.Event{
			V: events.Version, TS: next(), Kind: kind, Project: project,
			Entity:  events.EntityRef{Type: events.EntityTask, ID: id},
			Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
			Payload: raw,
		})
	}
	created := func(id, status, plan string) {
		emit(events.KindTaskCreated, id, events.TaskCreatedPayload{
			Title: "t" + id, Phase: "now", Status: status, Type: "todo", Plan: plan,
		})
	}

	// Filed at untriaged, without and with a plan.
	created("1", "untriaged", "")
	created("2", "untriaged", "# a plan")
	// The description-edit reset, on a task without and with a plan.
	created("3", "ready", "")
	emit(events.KindTaskFieldsUpdated, "3", events.TaskFieldsUpdatedPayload{
		Fields: map[string]any{"description": "re-spec", "status": "untriaged"}})
	created("4", "submitted", "# a plan")
	emit(events.KindTaskFieldsUpdated, "4", events.TaskFieldsUpdatedPayload{
		Fields: map[string]any{"description": "re-spec", "status": "untriaged"}})
	// Reconsidering a declined task.
	created("5", "unplanned", "")
	emit(events.KindTaskStatusChanged, "5", events.TaskStatusChangedPayload{
		OldStatus: "unplanned", NewStatus: "declined", Reason: "no"})
	emit(events.KindTaskStatusChanged, "5", events.TaskStatusChangedPayload{
		OldStatus: "declined", NewStatus: "untriaged"})

	tempPath, result, err := events.ProjectToTempDB(dir)
	if err != nil {
		t.Fatalf("ProjectToTempDB: %v", err)
	}
	t.Cleanup(func() { os.Remove(tempPath) })
	if len(result.Errors) > 0 {
		t.Fatalf("projection reported errors: %v", result.Errors)
	}
	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	want := map[int]string{1: "unplanned", 2: "submitted", 3: "unplanned", 4: "submitted", 5: "unplanned"}
	for id, status := range want {
		var got string
		if err := db.QueryRow("SELECT status FROM tasks WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatalf("E-%d: %v", id, err)
		}
		if got != status {
			t.Errorf("E-%d projected %q, want %q", id, got, status)
		}
	}
}
