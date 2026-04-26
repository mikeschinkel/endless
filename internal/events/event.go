// Package events defines the event envelope schema and closed event-kind
// vocabulary for Endless's event-sourcing system. Every state-changing
// operation produces one Event appended to a segmented JSONL file.
package events

import (
	"encoding/json"
	"fmt"

	"github.com/mikeschinkel/endless/internal/kairos"
)

// Version is the current event envelope schema version.
const Version = 1

// Event is the fixed envelope wrapping every state-changing event.
type Event struct {
	V             int             `json:"v"`
	TS            string          `json:"ts"`
	Kind          Kind            `json:"kind"`
	Project       string          `json:"project"`
	Entity        EntityRef       `json:"entity"`
	Actor         Actor           `json:"actor"`
	CorrelationID string          `json:"cid,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

// EntityRef identifies the primary entity affected by an event.
type EntityRef struct {
	Type EntityType `json:"type"`
	ID   string     `json:"id"`
}

// Actor identifies who or what produced an event.
type Actor struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

// Kind is a closed enumeration of event types.
type Kind string

// EntityType enumerates entity nouns.
type EntityType string

const (
	EntityTask         EntityType = "task"
	EntityTaskDep      EntityType = "task_dep"
	EntityProject      EntityType = "project"
	EntitySession      EntityType = "session"
	EntityConversation EntityType = "conversation"
	EntityMessage      EntityType = "message"
	EntityNote         EntityType = "note"
)

// ActorKind enumerates actor categories.
type ActorKind string

const (
	ActorSession ActorKind = "session"
	ActorCLI     ActorKind = "cli"
	ActorHook    ActorKind = "hook"
	ActorSystem  ActorKind = "system"
	ActorWeb     ActorKind = "web"
)

// Task event kinds.
const (
	KindTaskCreated       Kind = "task.created"
	KindTaskImported      Kind = "task.imported"
	KindTaskStatusChanged Kind = "task.status_changed"
	KindTaskFieldsUpdated Kind = "task.fields_updated"
	KindTaskMoved         Kind = "task.moved"
	KindTaskDeleted       Kind = "task.deleted"
	KindTaskBulkCleared   Kind = "task.bulk_cleared"
)

// Task dependency event kinds.
const (
	KindTaskDepCreated Kind = "task_dep.created"
	KindTaskDepDeleted Kind = "task_dep.deleted"
)

// Project event kinds.
const (
	KindProjectRegistered   Kind = "project.registered"
	KindProjectUpdated      Kind = "project.updated"
	KindProjectRenamed      Kind = "project.renamed"
	KindProjectUnregistered Kind = "project.unregistered"
	KindProjectPurged       Kind = "project.purged"
)

// Session event kinds.
const (
	KindSessionWorkStarted   Kind = "session.work_started"
	KindSessionChatStarted   Kind = "session.chat_started"
	KindSessionIdled         Kind = "session.idled"
	KindSessionEnded         Kind = "session.ended"
	KindSessionTaskCompleted Kind = "session.task_completed"
	KindSessionRecapped      Kind = "session.recapped"
	KindSessionHidden        Kind = "session.hidden"
)

// Conversation event kinds.
const (
	KindConversationBeaconed  Kind = "conversation.beaconed"
	KindConversationConnected Kind = "conversation.connected"
	KindConversationClosed    Kind = "conversation.closed"
)

// Message event kinds.
const (
	KindMessageSent      Kind = "message.sent"
	KindMessageDelivered Kind = "message.delivered"
)

// Note event kinds.
const (
	KindNoteCreated  Kind = "note.created"
	KindNoteResolved Kind = "note.resolved"
)

// ValidKinds is the closed set of all recognized event kinds.
var ValidKinds = map[Kind]bool{
	// Task
	KindTaskCreated:       true,
	KindTaskImported:      true,
	KindTaskStatusChanged: true,
	KindTaskFieldsUpdated: true,
	KindTaskMoved:         true,
	KindTaskDeleted:       true,
	KindTaskBulkCleared:   true,
	// Task dependency
	KindTaskDepCreated: true,
	KindTaskDepDeleted: true,
	// Project
	KindProjectRegistered:   true,
	KindProjectUpdated:      true,
	KindProjectRenamed:      true,
	KindProjectUnregistered: true,
	KindProjectPurged:       true,
	// Session
	KindSessionWorkStarted:   true,
	KindSessionChatStarted:   true,
	KindSessionIdled:         true,
	KindSessionEnded:         true,
	KindSessionTaskCompleted: true,
	KindSessionRecapped:      true,
	KindSessionHidden:        true,
	// Conversation
	KindConversationBeaconed:  true,
	KindConversationConnected: true,
	KindConversationClosed:    true,
	// Message
	KindMessageSent:      true,
	KindMessageDelivered: true,
	// Note
	KindNoteCreated:  true,
	KindNoteResolved: true,
}

// validEntityTypes is the closed set of recognized entity types.
var validEntityTypes = map[EntityType]bool{
	EntityTask:         true,
	EntityTaskDep:      true,
	EntityProject:      true,
	EntitySession:      true,
	EntityConversation: true,
	EntityMessage:      true,
	EntityNote:         true,
}

// validActorKinds is the closed set of recognized actor kinds.
var validActorKinds = map[ActorKind]bool{
	ActorSession: true,
	ActorCLI:     true,
	ActorHook:    true,
	ActorSystem:  true,
	ActorWeb:     true,
}

// Validate checks that the event envelope is well-formed.
func (e *Event) Validate() error {
	if e.V != Version {
		return fmt.Errorf("events: unsupported version %d, want %d", e.V, Version)
	}
	if _, err := kairos.Parse(e.TS); err != nil {
		return fmt.Errorf("events: invalid ts: %w", err)
	}
	if !ValidKinds[e.Kind] {
		return fmt.Errorf("events: unknown kind %q", e.Kind)
	}
	if !validEntityTypes[e.Entity.Type] {
		return fmt.Errorf("events: unknown entity type %q", e.Entity.Type)
	}
	if e.Entity.ID == "" {
		return fmt.Errorf("events: entity id is empty")
	}
	if !validActorKinds[e.Actor.Kind] {
		return fmt.Errorf("events: unknown actor kind %q", e.Actor.Kind)
	}
	if e.Actor.ID == "" {
		return fmt.Errorf("events: actor id is empty")
	}
	if e.Payload == nil {
		return fmt.Errorf("events: payload is nil")
	}
	return nil
}
