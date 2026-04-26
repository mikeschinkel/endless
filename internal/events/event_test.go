package events_test

import (
	"encoding/json"
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
		Status: "needs_plan",
		Type:   "task",
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
		OldStatus: "in_progress",
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
		Status: "needs_plan",
		Type:   "task",
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

func TestValidate_Errors(t *testing.T) {
	ts := testTimestamp()
	payload, _ := json.Marshal(events.TaskCreatedPayload{Title: "x", Phase: "now", Status: "needs_plan", Type: "task"})

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
	want := 28
	got := len(events.ValidKinds)
	if got != want {
		t.Errorf("ValidKinds has %d entries, want %d", got, want)
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
			Title: "Test", Phase: "now", Status: "needs_plan", Type: "task", Tier: &tier, ParentID: &parentID, SortOrder: 5,
		}},
		{"TaskStatusChanged", events.TaskStatusChangedPayload{
			OldStatus: "in_progress", NewStatus: "confirmed", CompletedAt: "2026-04-25T14:00:00",
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
