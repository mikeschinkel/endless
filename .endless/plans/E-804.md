# E-804: Define Event Envelope and Closed Event-Kind Vocabulary

## Context

E-804 is the second task on the critical path for event-sourcing (E-799), immediately after kairos (E-803, done). Before any event can be written (E-805), we need: the JSON envelope schema that wraps every event, the closed vocabulary of event kinds, and the Go types that represent them. This task defines the **spec and types only** — actual event emission is E-805.

## Scope

- New package: `internal/events/`
- Go types: `Event`, `Kind` constants, `EntityRef`, `Actor`, payload structs
- No Python changes (Python shells out to Go for event writing in E-805)
- No file I/O, no segment writing — just type definitions and constants
- Tests validate JSON serialization round-trips and kind string consistency

## Event Envelope

Every state-changing operation produces one JSONL line with this structure:

```json
{"v":1,"ts":"0C8G1T3K0A1B2C3","kind":"task.created","project":"endless","entity":{"type":"task","id":"804"},"actor":{"kind":"session","id":"a1b2c3d4-..."},"payload":{...}}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `v` | int | yes | Schema version, always `1` |
| `ts` | string | yes | Kairos timestamp (15-char). Serves as both timestamp and unique event ID |
| `kind` | string | yes | Closed vocabulary. Format: `entity.verb_past_tense` |
| `project` | string | yes | Project name (stable across rebuilds, unlike integer IDs) |
| `entity` | object | yes | `{"type":"task","id":"804"}` — primary entity affected |
| `actor` | object | yes | `{"kind":"session","id":"..."}` — who/what produced the event |
| `cid` | string | no | Correlation ID: links related events from a single action (e.g., cascade complete). Kairos timestamp of the first event in the batch |
| `payload` | object | yes | Kind-specific data |

### Design decisions

- **`ts` is the event ID** — kairos timestamps are unique per node (monotonic + node ID embedded). No separate `id` field needed.
- **Project by name, not integer ID** — integer IDs don't survive projection rebuilds. Names are the stable identifier.
- **Entity ID as string** — integer IDs formatted as decimal strings (`"804"`), UUIDs as full strings. Avoids `interface{}`.
- **One event per affected entity for cascades** — cascade complete emits N `task.status_changed` events linked by `cid`. Each task's change is independently replayable.

## Go Types

```
internal/events/
    event.go      — Event struct, Kind/EntityType/ActorKind constants
    payload.go    — Payload structs per event kind
    event_test.go — (package events_test) JSON round-trip, kind validation
```

### event.go

```go
package events

const Version = 1

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

type Kind string

type EntityRef struct {
    Type EntityType `json:"type"`
    ID   string     `json:"id"`
}

type EntityType string

const (
    EntityTask         EntityType = "task"
    EntityTaskDep      EntityType = "task_dep"
    EntityProject      EntityType = "project"
    EntitySession      EntityType = "session"
    EntityConversation EntityType = "conversation"
    EntityMessage      EntityType = "message"
    EntityNote         EntityType = "note"
    EntityActivity     EntityType = "activity"
)

type Actor struct {
    Kind ActorKind `json:"kind"`
    ID   string    `json:"id"`
}

type ActorKind string

const (
    ActorSession ActorKind = "session"
    ActorCLI     ActorKind = "cli"
    ActorHook    ActorKind = "hook"
    ActorSystem  ActorKind = "system"
    ActorWeb     ActorKind = "web"
)
```

## Event Kind Vocabulary (27 kinds across 7 entity types)

### Task events (7 kinds)

| Kind | Trigger | Payload |
|---|---|---|
| `task.created` | `add_item` | title, description, phase, status, type, tier, parent_id, sort_order |
| `task.imported` | `import_plan`, `import_json` (one per task) | title, description, phase, status, source_file, sort_order, parent_id |
| `task.status_changed` | `complete_item`, `assume_item`, `start_item`, status via `update_plan` | old_status, new_status, completed_at |
| `task.fields_updated` | `update_plan` (non-status fields) | fields map (only changed keys: title, description, text, prompt, phase, tier, type) |
| `task.moved` | `move_task` | old_parent_id, new_parent_id |
| `task.deleted` | `remove_item` | cascade (bool), title (audit trail) |
| `task.bulk_cleared` | `import_plan(replace=True)`, `import_json(clear=True)` | source_file |

### Task dependency events (2 kinds)

| Kind | Trigger | Payload |
|---|---|---|
| `task_dep.created` | `add_dep` | source_id, target_id, dep_type |
| `task_dep.deleted` | `remove_dep` | source_id, target_id |

Entity ID: `"{source_id}:{target_id}"` (composite key).

### Project events (5 kinds)

| Kind | Trigger | Payload |
|---|---|---|
| `project.registered` | `register_project` (new) | name, label, path, group_name, description, status, language |
| `project.updated` | `set_field`, `reconcile` | fields map (changed keys) |
| `project.renamed` | `rename_project` | old_name, new_name |
| `project.unregistered` | `unregister_project` | name |
| `project.purged` | `purge_project` | name, path |

### Session events (7 kinds)

| Kind | Trigger | Payload |
|---|---|---|
| `session.work_started` | `StartWorkSession` | task_id, process |
| `session.chat_started` | `StartChatSession`, `start_chat` | process |
| `session.idled` | `IdleSession` | — |
| `session.ended` | `EndSession` | — |
| `session.task_completed` | `CompleteTask` (session side) | task_id |
| `session.recapped` | `RecapSession` | summary |
| `session.hidden` | `hide_sessions` | — |

### Conversation events (3 kinds)

| Kind | Trigger | Payload |
|---|---|---|
| `conversation.beaconed` | `CreateBeacon` | process_a |
| `conversation.connected` | `ConnectToConversation` | process_b |
| `conversation.closed` | `CloseConversation` | — |

### Message events (2 kinds)

| Kind | Trigger | Payload |
|---|---|---|
| `message.sent` | `SendMessage` | conversation_id, sender, body |
| `message.delivered` | `GetPendingMessages` | conversation_id |

### Note events (2 kinds)

| Kind | Trigger | Payload |
|---|---|---|
| `note.created` | `add_note` | note_type, message |
| `note.resolved` | `resolve_note` | — |

### Excluded from events (ephemeral/operational)

- `TouchSession` — heartbeat, no domain state change
- `BackfillProcess` — idempotent backfill
- `SetTranscriptPath`, `SetPlanFilePath`, `SetProcess` — operational metadata, not domain state
- `FlagForRecap` — internal scheduling flag
- Activity recording — deferred; activity is already a projection of hook data
- Channel register/unregister — ephemeral port registry, not persistent state

## Payload Structs

Defined in `payload.go`. Each event kind has a corresponding struct. Key patterns:

- `*FieldsUpdatedPayload` uses `map[string]any` for flexibility (task and project share this pattern)
- Status change payloads carry `old_status` and `new_status` for auditability
- Empty payloads (session.idled, session.ended, note.resolved) use `struct{}` — the entity ref carries the ID
- Create payloads carry all initial field values for full replay

## Validation

`event.go` includes a `ValidKinds` set and a `Validate()` method on Event that checks:
- `v == Version`
- `kind` is in the closed vocabulary
- `ts` is parseable as kairos
- `entity.type` is a known EntityType
- `actor.kind` is a known ActorKind

## Files to Create

- `internal/events/event.go` — Event struct, Kind/EntityType/ActorKind constants, ValidKinds, Validate()
- `internal/events/payload.go` — all payload structs
- `internal/events/event_test.go` — (package events_test) JSON marshal/unmarshal round-trip, Validate() cases

## Verification

```bash
go test ./internal/events/... -v    # types compile, JSON round-trips, validation works
go build ./internal/events/...      # no import cycles
```
