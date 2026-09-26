package events_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
)

func testTimestamp() string {
	c := kairos.NewClock(0x1234)
	return c.Now().String()
}

func TestEventJSONRoundTrip(t *testing.T) {
	ts := testTimestamp()

	payload, _ := json.Marshal(events.TaskCreatedPayload{
		Title:  "Implement kairos",
		Phase:  "now",
		Status: "unplanned",
		Type:   "todo",
	})

	evt := events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    events.KindTaskCreated,
		Project: "endless",
		Entity: events.EntityRef{
			Type: events.EntityTask,
			ID:   "803",
		},
		Actor: events.Actor{
			Kind: events.ActorCLI,
			ID:   "mike@macbook",
		},
		Payload: payload,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed events.Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if parsed.V != evt.V {
		t.Errorf("V = %d, want %d", parsed.V, evt.V)
	}
	if parsed.TS != evt.TS {
		t.Errorf("TS = %q, want %q", parsed.TS, evt.TS)
	}
	if parsed.Kind != evt.Kind {
		t.Errorf("Kind = %q, want %q", parsed.Kind, evt.Kind)
	}
	if parsed.Project != evt.Project {
		t.Errorf("Project = %q, want %q", parsed.Project, evt.Project)
	}
	if parsed.Entity.Type != evt.Entity.Type {
		t.Errorf("Entity.Type = %q, want %q", parsed.Entity.Type, evt.Entity.Type)
	}
	if parsed.Entity.ID != evt.Entity.ID {
		t.Errorf("Entity.ID = %q, want %q", parsed.Entity.ID, evt.Entity.ID)
	}
	if parsed.Actor.Kind != evt.Actor.Kind {
		t.Errorf("Actor.Kind = %q, want %q", parsed.Actor.Kind, evt.Actor.Kind)
	}
	if parsed.CorrelationID != "" {
		t.Errorf("CorrelationID = %q, want empty", parsed.CorrelationID)
	}
}

func TestEventJSONRoundTrip_WithCorrelation(t *testing.T) {
	c := kairos.NewClock(0x1234)
	ts1 := c.Now().String()
	ts2 := c.Now().String()

	payload, _ := json.Marshal(events.TaskStatusChangedPayload{
		OldStatus: "underway",
		NewStatus: "confirmed",
	})

	evt := events.Event{
		V:       events.Version,
		TS:      ts2,
		Kind:    events.KindTaskStatusChanged,
		Project: "endless",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "496"},
		Actor:   events.Actor{Kind: events.ActorSession, ID: "abc-123"},
		CorrelationID: ts1,
		Payload: payload,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var parsed events.Event
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if parsed.CorrelationID != ts1 {
		t.Errorf("CorrelationID = %q, want %q", parsed.CorrelationID, ts1)
	}
}

func TestEventJSONLine(t *testing.T) {
	ts := testTimestamp()
	payload, _ := json.Marshal(events.NoteResolvedPayload{})

	evt := events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    events.KindNoteResolved,
		Project: "endless",
		Entity:  events.EntityRef{Type: events.EntityNote, ID: "42"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "mike@macbook"},
		Payload: payload,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// JSONL: single line, no embedded newlines
	for _, b := range data {
		if b == '\n' {
			t.Fatal("JSON output contains newline; not valid JSONL")
		}
	}
}

func TestValidate_Valid(t *testing.T) {
	ts := testTimestamp()
	payload, _ := json.Marshal(events.TaskCreatedPayload{
		Title:  "Test task",
		Phase:  "now",
		Status: "unplanned",
		Type:   "todo",
	})

	evt := events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    events.KindTaskCreated,
		Project: "endless",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "1"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "mike@macbook"},
		Payload: payload,
	}

	if err := evt.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

// TestValidate_ActorTriager pins E-1859's addition: a transition decided by a
// model validates as its own actor kind, carries no session (there is none to
// attribute to), and does so WITHOUT any of the pre-existing kinds having to
// change — the whole point of making the change additive is that no historical
// event needs upcasting.
func TestValidate_ActorTriager(t *testing.T) {
	payload, _ := json.Marshal(events.TaskStatusChangedPayload{
		OldStatus: "untriaged", NewStatus: "submitted",
	})
	evt := events.Event{
		V:       events.Version,
		TS:      testTimestamp(),
		Kind:    events.KindTaskStatusChanged,
		Project: "endless",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "1859"},
		Actor:   events.Actor{Kind: events.ActorTriager, ID: "mike@macbook"},
		Payload: payload,
	}
	if err := evt.Validate(); err != nil {
		t.Errorf("triager actor kind rejected: %v", err)
	}
	if evt.Actor.SessionID != "" {
		t.Error("a triager event must carry no session id")
	}

	for _, kind := range []events.ActorKind{
		events.ActorSession, events.ActorCLI, events.ActorHook,
		events.ActorSystem, events.ActorWeb,
	} {
		e := evt
		e.Actor.Kind = kind
		if err := e.Validate(); err != nil {
			t.Errorf("pre-existing actor kind %q broke: %v", kind, err)
		}
	}
}

func TestValidate_Errors(t *testing.T) {
	ts := testTimestamp()
	payload, _ := json.Marshal(events.TaskCreatedPayload{Title: "x", Phase: "now", Status: "unplanned", Type: "todo"})

	base := events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    events.KindTaskCreated,
		Project: "endless",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "1"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "mike@macbook"},
		Payload: payload,
	}

	tests := []struct {
		name   string
		mutate func(*events.Event)
	}{
		{"bad version", func(e *events.Event) { e.V = 99 }},
		{"bad ts", func(e *events.Event) { e.TS = "not-a-kairos" }},
		{"unknown kind", func(e *events.Event) { e.Kind = "bogus.kind" }},
		{"unknown entity type", func(e *events.Event) { e.Entity.Type = "bogus" }},
		{"empty entity id", func(e *events.Event) { e.Entity.ID = "" }},
		{"unknown actor kind", func(e *events.Event) { e.Actor.Kind = "bogus" }},
		{"empty actor id", func(e *events.Event) { e.Actor.ID = "" }},
		{"nil payload", func(e *events.Event) { e.Payload = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := base
			tt.mutate(&evt)
			if err := evt.Validate(); err == nil {
				t.Error("Validate() = nil, want error")
			}
		})
	}
}

func TestValidKinds_Count(t *testing.T) {
	// Ensures we don't accidentally add a kind constant without registering it.
	// Update this count when adding new kinds.
	// 33 (pre-E-1378) + 7 (decision.{created,fields_updated,accepted,rejected,deleted}
	// + decision_relation.{created,deleted}) + 1 (E-1541 epic.status_derived)
	// + 1 (E-1683 session_tasks.ordered)
	// + 2 (E-1864 decision.{unaccepted,unrejected})
	// - 1 (E-1906 session.recapped, retired with the session-recap machinery)
	// + 2 (E-1696 session_tasks.{queued,removed})
	// + 3 (E-1920 decision.{superseded,obsoleted,reinstated})
	// - 3 (E-2142 task.bulk_cleared, session_tasks.ordered, project_next.revised —
	//      moved to RetiredKinds, not deleted: see TestRetiredKinds below)
	// + 1 (E-2173 session_tasks.touched)
	// + 2 (E-2176 task.questions_asked, task_question.resolved) = 48.
	want := 48
	got := len(events.ValidKinds)
	if got != want {
		t.Errorf("ValidKinds has %d entries, want %d", got, want)
	}
}

// TestRetiredKinds pins the three invariants that make E-2142's retired-kinds
// list worth having over the two shortcuts it was chosen against — dropping the
// kinds outright (replay breaks) and ignoring every unrecognized kind (a typo
// becomes silence).
func TestRetiredKinds(t *testing.T) {
	want := []events.Kind{
		events.KindTaskBulkCleared,
		events.KindSessionTasksOrdered,
		events.KindProjectNextRevised,
	}
	if len(events.RetiredKinds) != len(want) {
		t.Errorf("RetiredKinds has %d entries, want %d", len(events.RetiredKinds), len(want))
	}

	for _, kind := range want {
		if !events.RetiredKinds[kind] {
			t.Errorf("%q missing from RetiredKinds", kind)
		}
		// Disjoint from ValidKinds, which is what keeps a retired kind
		// unemittable: the executor and `event emit` both gate on ValidKinds.
		if events.ValidKinds[kind] {
			t.Errorf("%q is in ValidKinds; a retired kind must not be emittable", kind)
		}
		// …and still KNOWN, which is what keeps a historical ledger replayable.
		if !events.KnownKind(kind) {
			t.Errorf("KnownKind(%q) = false; a retired kind must still validate", kind)
		}
	}

	// The property the named list exists to preserve.
	if events.KnownKind("project_next.rebalanced") {
		t.Error("KnownKind() accepted an undeclared kind; retirement must not become a wildcard")
	}
}

// TestValidate_RetiredKindsPassAndUndeclaredDoNot asserts the invariants above
// where they are actually consumed: Event.Validate, the first gate every event a
// rebuild replays has to pass. A retired kind that satisfied KnownKind but
// tripped Validate would still make a historical ledger unreplayable, which is
// the whole thing E-2142 had to avoid.
func TestValidate_RetiredKindsPassAndUndeclaredDoNot(t *testing.T) {
	base := events.Event{
		V:       events.Version,
		TS:      testTimestamp(),
		Project: "endless",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "1"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "mike@macbook"},
		Payload: json.RawMessage(`{}`),
	}

	// Each retired kind carries the entity type its deleted writer really used —
	// a kind that validates against an entity type that does not is no use.
	for kind, entity := range map[events.Kind]events.EntityType{
		events.KindTaskBulkCleared:     events.EntityTask,
		events.KindSessionTasksOrdered: events.EntitySessionTasks,
		events.KindProjectNextRevised:  events.EntityProjectNext,
	} {
		evt := base
		evt.Kind = kind
		evt.Entity = events.EntityRef{Type: entity, ID: "1"}
		if err := evt.Validate(); err != nil {
			t.Errorf("Validate() on retired kind %q = %v, want nil", kind, err)
		}
	}

	// Plausible, never declared. This is the failure retiring a kind must not
	// suppress: the alternative design — ignore anything unrecognized — would
	// accept this and lose every typo and every kind from a newer binary with it.
	evt := base
	evt.Kind = "project_next.rebalanced"
	err := evt.Validate()
	if err == nil {
		t.Fatal("Validate() = nil for an undeclared kind, want the unknown-kind error")
	}
	if !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("Validate() error = %q, want it to name an unknown kind", err)
	}
}

func TestPayloadRoundTrips(t *testing.T) {
	// Verify that each payload type marshals and unmarshals correctly.
	tier := 2
	parentID := int64(100)

	tests := []struct {
		name    string
		payload any
	}{
		{"TaskCreated", events.TaskCreatedPayload{
			Title: "Test", Phase: "now", Status: "unplanned", Type: "todo", Tier: &tier, ParentID: &parentID, SortOrder: 5,
		}},
		{"TaskStatusChanged", events.TaskStatusChangedPayload{
			OldStatus: "underway", NewStatus: "confirmed", CompletedAt: "2026-04-25T14:00:00",
		}},
		{"TaskFieldsUpdated", events.TaskFieldsUpdatedPayload{
			Fields: map[string]any{"title": "New title", "tier": float64(3)},
		}},
		{"TaskMoved", events.TaskMovedPayload{OldParentID: &parentID, NewParentID: nil}},
		{"TaskDeleted", events.TaskDeletedPayload{Cascade: true, Title: "Removed task"}},
		{"TaskDepCreated", events.TaskDepCreatedPayload{SourceID: 10, TargetID: 20, DepType: "needs"}},
		{"ProjectRegistered", events.ProjectRegisteredPayload{
			Name: "endless", Path: "/Users/mike/Projects/endless", Status: "active",
		}},
		{"ProjectRenamed", events.ProjectRenamedPayload{OldName: "old", NewName: "new"}},
		{"SessionWorkStarted", events.SessionWorkStartedPayload{TaskID: 804, Process: "tmux:1"}},
		{"ConversationBeaconed", events.ConversationBeaconedPayload{ProcessA: "tmux:1"}},
		{"MessageSent", events.MessageSentPayload{ConversationID: "abc", Sender: "tmux:1", Body: "hello"}},
		{"NoteCreated", events.NoteCreatedPayload{NoteType: "general", Message: "a note"}},
		{"NoteResolved", events.NoteResolvedPayload{}},
		{"SessionIdled", events.SessionIdledPayload{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.payload)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			// Verify it produces valid JSON
			var raw json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("produced invalid JSON: %v", err)
			}
		})
	}
}

// Suppress unused import warning for time (used by testTimestamp indirectly via kairos).
var _ = time.Now
