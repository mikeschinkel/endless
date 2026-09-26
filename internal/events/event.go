// Package events defines the event envelope schema and closed event-kind
// vocabulary for Endless's event-sourcing system. Every state-changing
// operation produces one Event appended to a segmented JSONL file.
package events

import (
	"encoding/json"
	"fmt"

	"github.com/mikeschinkel/endless/internal/agentenv"
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
//
// SessionID is optional context: when the event was emitted from
// within a Claude session's reach (CLI runs in a Claude pane, or
// the Claude hook fires), SessionID is the numeric Endless session
// id ("356"). When empty (e.g. system events, manual sqlite edits,
// pre-2026-05-12 events), the actor is not session-attributable.
//
// Kind/ID still describe WHERE the event came from (cli, hook, web).
// SessionID separately captures WHICH session it was part of, when
// known. The two are orthogonal — a cli actor with a session_id
// means "the user ran a CLI command from inside a Claude session."
type Actor struct {
	Kind      ActorKind `json:"kind"`
	ID        string    `json:"id"`
	SessionID string    `json:"session_id,omitempty"`

	// Harness names the agent host that emitted this event — an agentenv.ID
	// ("claude_cli"), or "" when no agent harness was detected, meaning a
	// person at a shell (E-2005).
	//
	// It answers "WHO did this", which none of the three fields above can.
	// Kind names the CHANNEL, and `cli` is produced identically by a human
	// typing `endless worktree land` and an agent shelling out to the same
	// command. SessionID is worse than useless for the question: it answers
	// "which session is this ABOUT", and its resolver deliberately credits a
	// bare shell in a sibling tmux pane to the Claude session next to it
	// (E-1294), so a human's command routinely arrives carrying the agent's
	// own session id.
	//
	// Orthogonal to Kind on purpose. `hook` is always an agent, `web` never
	// is, and `cli` is the ambiguous one this resolves; folding agent-ness
	// into Kind would multiply the enum and break the closed set every
	// consumer switches on.
	//
	// Stamped in Go at emit time by EmittingActor from the environment this
	// process inherited, never passed in as a flag: the emitting process is
	// the authoritative observer, and a value the caller supplies is a value a
	// stale or hand-rolled caller can get wrong.
	//
	// Purely additive — every historical event stays valid unchanged, the same
	// argument ActorTriager makes in its own comment.
	Harness string `json:"harness,omitempty"`
}

// EmittingActor builds the Actor for an event THIS process is about to emit,
// stamping Harness from the environment (E-2005).
//
// Every emit path that takes its actor from the caller goes through here, so
// "the harness is observed, not declared" is a property of one function rather
// than a convention three call sites have to remember.
//
// Deliberately not used by the epic-derivation emitter in internal/eventcmd:
// that event is synthesized by Endless as a consequence of another mutation and
// is attributed to the system actor. "Which harness typed it" has no answer
// there, and "" — no agent — would be the wrong one.
func EmittingActor(kind ActorKind, id, sessionID string) Actor {
	return Actor{
		Kind:      kind,
		ID:        id,
		SessionID: sessionID,
		Harness:   DetectedHarness(),
	}
}

// DetectedHarness names the agent harness running this process, or "" when
// there is none.
//
// The empty string rather than agentenv.Unknown: "" is what `omitempty` drops
// from the envelope, so a human's event carries no harness key at all, and
// downstream SQL can spell "a human did this" as IS NULL. Recording the literal
// "unknown" would make every historical event that predates this field look
// different from a human's, which is the one distinction that has to hold.
//
// Spelled through agentenv.Present rather than repeating `Detect() != Unknown`
// (E-2006): that comparison IS the "an agent did this" predicate, and it is
// also what internal/monitor needs for the two stamps that bypass the executor.
// Two hand-written copies of one comparison is how the divergence E-2006 closed
// got in.
func DetectedHarness() string {
	if !agentenv.Present() {
		return ""
	}
	return string(agentenv.Detect())
}

// Kind is a closed enumeration of event types.
type Kind string

// EntityType enumerates entity nouns.
type EntityType string

const (
	EntityTask    EntityType = "task"
	EntityTaskDep EntityType = "task_dep"
	EntityProject EntityType = "project"
	// EntityProjectNext has no live writer (E-2142 retired the curated next
	// list). It stays declared because the db-ledger still holds
	// project_next.revised events, and a retired KIND that validates is no use
	// if its entity type does not — Validate checks both.
	EntityProjectNext   EntityType = "project_next"
	EntitySession       EntityType = "session"
	EntitySessionStatus EntityType = "session_status"
	EntitySessionTasks  EntityType = "session_tasks" // E-1683

	EntityConversation     EntityType = "conversation"
	EntityMessage          EntityType = "message"
	EntityNote             EntityType = "note"
	EntityDecision         EntityType = "decision"          // E-1378
	EntityDecisionRelation EntityType = "decision_relation" // E-1378
	EntityTaskQuestion     EntityType = "task_question"     // E-2176
)

// ActorKind enumerates actor categories.
type ActorKind string

const (
	ActorSession ActorKind = "session"
	ActorCLI     ActorKind = "cli"
	ActorHook    ActorKind = "hook"
	ActorSystem  ActorKind = "system"
	ActorWeb     ActorKind = "web"

	// ActorTriager marks a transition decided by a model rather than by a
	// person or by a person's command (E-1859). It is deliberately not folded
	// into ActorSystem: "a machine decided this" is a distinct, queryable claim
	// from "cron/migration did this", and once triage starts making calls a
	// user disagrees with, `WHERE actor.kind = 'triager'` is the query they
	// need. Purely additive — every historical event stays valid under the
	// kinds above, so nothing needs upcasting. Like ActorSystem and ActorWeb it
	// carries no session (nothing to attribute to); the deciding model and its
	// one-line rationale ride in the payload as provenance, which is a separate
	// concern from attribution.
	ActorTriager ActorKind = "triager"
)

// Task event kinds.
const (
	KindTaskCreated       Kind = "task.created"
	KindTaskImported      Kind = "task.imported"
	KindTaskStatusChanged Kind = "task.status_changed"
	KindTaskFieldsUpdated Kind = "task.fields_updated"
	KindTaskMoved         Kind = "task.moved"
	KindTaskDeleted       Kind = "task.deleted"
	KindTaskReleased      Kind = "task.released"
	KindTaskClaimed       Kind = "task.claimed"
	KindTaskLanded        Kind = "task.landed"
)

// Task question event kinds (E-2176).
//
//   - task.questions_asked: one event per SERIES, entity = the task. The ids and
//     the series number are allocated under the write lock by `event emit` and
//     written into the payload before the ledger append, so a replay inserts
//     exactly the rows the live path did instead of recomputing either.
//   - task_question.resolved: one event per question, entity = the question.
//     One kind for every way out of `open` (answered, withdrawn, invalid,
//     superseded): the payload's status names which, and the lifecycle guard in
//     internal/questionstatus decides whether the move is legal.
const (
	KindTaskQuestionsAsked   Kind = "task.questions_asked"
	KindTaskQuestionResolved Kind = "task_question.resolved"
)

// Task dependency event kinds.
const (
	KindTaskDepCreated Kind = "task_dep.created"
	KindTaskDepDeleted Kind = "task_dep.deleted"
)

// Epic derivation event kinds (E-1541). An epic's status is auto-derived from
// its children's statuses; epic.status_derived records each derivation so the
// audit trail stays distinct from human-triggered task.status_changed events
// and the projector can reproduce derivation by replaying the recorded events.
const (
	KindEpicStatusDerived Kind = "epic.status_derived"
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

// Session status event kinds (E-1312).
const (
	KindSessionStatusRecorded Kind = "session_status.recorded"
)

// Session task-membership event kinds (E-1696, E-2173). The verbs that add a
// task to, and drop a task from, the emitting session's scope — the correction
// path for the otherwise-automatic session_tasks capture.
//
//   - queued:  `session task add` promotes tasks to relation `queued` (decided
//     session work). Goes through upsertSessionTask, so it obeys the same
//     upgrade-only ladder as every automatic capture and cannot demote a goal.
//   - touched: `touch` enrolls tasks at relation `revisited` (E-2173) — scope
//     entry with no claim on the session's agenda and no edit to the task.
//     Same ladder, same payload; only the relation differs from queued.
//   - removed: `session task remove` DELETES the session_tasks row outright.
//     Distinct from `session hide --task` (E-1914), which suppresses the row
//     from one session's listing while keeping the association — hide is for a
//     capture that is real but noisy, remove is for one that was simply wrong.
//
// All three are session-scoped live state, not replayed by rebuild-db, and all
// are deliberate low-volume acts — so they ride the committed ledger on
// E-1683's `session_tasks.ordered` precedent rather than waiting on the
// machine-user ledger split (E-1673). The high-volume automatic read capture is
// what needs that split, and it is not these.
const (
	KindSessionTasksQueued  Kind = "session_tasks.queued"
	KindSessionTasksTouched Kind = "session_tasks.touched"
	KindSessionTasksRemoved Kind = "session_tasks.removed"
)

// Decision event kinds (E-1378). Decisions are first-class items extracted
// from the tasks table; their lifecycle (proposed -> accepted | rejected, and
// back to proposed via unaccept / unreject) has no analog in the task event
// vocabulary.
//
// E-1864 added the two reversals. They are deliberately NOT a single
// "reopened" kind: a decision is never "open", and keeping the inverse of
// each forward transition distinct lets the executor guard on the status it
// undoes (so undoing the wrong one errors instead of silently succeeding) and
// keeps rejection_reason clearing on the unreject path where it belongs.
//
// E-1920 added the two END states, which are a different axis from accept /
// reject: those settle whether a decision was ADOPTED, these record that an
// adopted one STOPPED GOVERNING. Superseded names the decision that took over;
// obsoleted names what went away. Both apply only to `accepted`, so their
// single shared reversal (reinstated) has exactly one destination and needs no
// stored prior status.
const (
	KindDecisionCreated       Kind = "decision.created"
	KindDecisionFieldsUpdated Kind = "decision.fields_updated"
	KindDecisionAccepted      Kind = "decision.accepted"
	KindDecisionRejected      Kind = "decision.rejected"
	KindDecisionUnaccepted    Kind = "decision.unaccepted" // E-1864
	KindDecisionUnrejected    Kind = "decision.unrejected" // E-1864
	KindDecisionSuperseded    Kind = "decision.superseded" // E-1920
	KindDecisionObsoleted     Kind = "decision.obsoleted"  // E-1920
	KindDecisionReinstated    Kind = "decision.reinstated" // E-1920
	KindDecisionDeleted       Kind = "decision.deleted"
)

// Decision relation event kinds (E-1378). Source-table mapping: rows where
// the source is a decision live in decision_relations; task-sourced rows
// remain governed by task_dep events.
const (
	KindDecisionRelationCreated Kind = "decision_relation.created"
	KindDecisionRelationDeleted Kind = "decision_relation.deleted"
)

// Retired event kinds (E-2142). Each named the only surface that wrote it, and
// that surface is gone:
//
//   - task.bulk_cleared     — `task import --replace` / `task import-json --clear`
//   - session_tasks.ordered — `endless session order`
//   - project_next.revised  — `endless task next revise`
//
// The constants survive because the kinds do: the db-ledger is the permanent
// record, so events of all three are still in it and replay has to get past
// them. See RetiredKinds for what "getting past them" means.
const (
	KindTaskBulkCleared     Kind = "task.bulk_cleared"
	KindSessionTasksOrdered Kind = "session_tasks.ordered"
	KindProjectNextRevised  Kind = "project_next.revised"
)

// RetiredKinds is the closed set of kinds a historical ledger may hold and no
// live code path writes (E-2142).
//
// A retired kind VALIDATES and PROJECTS NOTHING, and that pairing is the whole
// design. Validating keeps a rebuild working on a ledger that predates the
// retirement; projecting nothing is the honest answer, because the tables those
// three events wrote no longer exist. Naming them in one declared list is what
// keeps the third property: a kind NOBODY declared — a typo, or a kind from a
// newer binary — still fails loudly on `events: unknown kind`. Silently
// ignoring every unrecognized kind would have been the cheap way to survive
// replay, and it would have thrown that away.
//
// Deliberately NOT folded into ValidKinds. The executor and the `event emit`
// front door both gate on ValidKinds alone, so a retired kind cannot be newly
// emitted — there is no writer left to emit it, and a kind whose executor was
// deleted should refuse at the door rather than reach a dispatch that would
// error on it internally.
//
// Retiring a KIND is not the same as retiring a COLUMN inside a live kind's
// payload, and E-2142 did both. `task.created` and `task.imported` still
// project in full; they simply stopped writing tasks.source_file. That
// retirement rides in the payload struct — the field is retained so an old
// event still unmarshals, and nothing reads the value — not here.
var RetiredKinds = map[Kind]bool{
	KindTaskBulkCleared:     true,
	KindSessionTasksOrdered: true,
	KindProjectNextRevised:  true,
}

// KnownKind reports whether a kind may appear in a well-formed event: a live
// kind, or a retired one that only replay will ever see.
func KnownKind(k Kind) bool {
	return ValidKinds[k] || RetiredKinds[k]
}

// ValidKinds is the closed set of event kinds a live code path may emit.
var ValidKinds = map[Kind]bool{
	// Task
	KindTaskCreated:       true,
	KindTaskImported:      true,
	KindTaskStatusChanged: true,
	KindTaskFieldsUpdated: true,
	KindTaskMoved:         true,
	KindTaskDeleted:       true,
	KindTaskReleased:      true,
	KindTaskClaimed:       true,
	KindTaskLanded:        true,
	// Task questions (E-2176)
	KindTaskQuestionsAsked:   true,
	KindTaskQuestionResolved: true,
	// Task dependency
	KindTaskDepCreated: true,
	KindTaskDepDeleted: true,
	// Epic derivation (E-1541)
	KindEpicStatusDerived: true,
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
	// Session status (E-1312)
	KindSessionStatusRecorded: true,
	// Session task membership (E-1696, E-2173)
	KindSessionTasksQueued:  true,
	KindSessionTasksTouched: true,
	KindSessionTasksRemoved: true,
	// Decision (E-1378)
	KindDecisionCreated:       true,
	KindDecisionFieldsUpdated: true,
	KindDecisionAccepted:      true,
	KindDecisionRejected:      true,
	KindDecisionUnaccepted:    true,
	KindDecisionUnrejected:    true,
	KindDecisionSuperseded:    true,
	KindDecisionObsoleted:     true,
	KindDecisionReinstated:    true,
	KindDecisionDeleted:       true,
	// Decision relation (E-1378)
	KindDecisionRelationCreated: true,
	KindDecisionRelationDeleted: true,
}

// validEntityTypes is the closed set of recognized entity types.
var validEntityTypes = map[EntityType]bool{
	EntityTask:             true,
	EntityTaskDep:          true,
	EntitySessionStatus:    true,
	EntitySessionTasks:     true,
	EntityProject:          true,
	EntityProjectNext:      true,
	EntitySession:          true,
	EntityConversation:     true,
	EntityMessage:          true,
	EntityNote:             true,
	EntityDecision:         true,
	EntityDecisionRelation: true,
	EntityTaskQuestion:     true,
}

// validActorKinds is the closed set of recognized actor kinds.
var validActorKinds = map[ActorKind]bool{
	ActorSession: true,
	ActorCLI:     true,
	ActorHook:    true,
	ActorSystem:  true,
	ActorWeb:     true,
	ActorTriager: true, // E-1859
}

// Validate checks that the event envelope is well-formed.
func (e *Event) Validate() error {
	if e.V != Version {
		return fmt.Errorf("events: unsupported version %d, want %d", e.V, Version)
	}
	if _, err := kairos.Parse(e.TS); err != nil {
		return fmt.Errorf("events: invalid ts: %w", err)
	}
	if !KnownKind(e.Kind) {
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
