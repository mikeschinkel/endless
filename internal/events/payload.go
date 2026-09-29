package events

import (
	"fmt"

	"github.com/mikeschinkel/endless/internal/rating"
)

// legacyPlanKey is the pre-E-1000 spelling of the plan field in event payloads.
// It survives only as a READ path: 1,257 db-ledger events carry it, the ledger
// is immutable, and a projector that ignored the key would rebuild those tasks
// with empty plans. Nothing emits it. Named here rather than repeated as a bare
// "text" so a future reader of either field map can find every site at once.
const legacyPlanKey = "text"

// Task payloads

type TaskCreatedPayload struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Plan        string `json:"plan,omitempty"`

	// LegacyText carries the pre-E-1000 spelling of Plan. 129 task.created
	// events in the db-ledger were emitted with a `text` key, and the ledger is
	// immutable by design — decoding them into Plan directly is impossible
	// (encoding/json takes one tag per field), and decoding them into nothing
	// would rebuild those tasks with empty plans, silently, because an absent
	// field is indistinguishable from an empty one.
	//
	// READ IT THROUGH PlanText(), never directly. Nothing emits this key any
	// more; `omitempty` keeps it out of every payload this struct marshals.
	LegacyText string `json:"text,omitempty"`

	Context  string `json:"context,omitempty"`
	Analysis string `json:"analysis,omitempty"`
	Notes    string `json:"notes,omitempty"`
	Phase    string `json:"phase"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	// Complexity and Risk are rating slugs (internal/rating) or absent for an
	// unrated task. Historical payloads carry a `tier` key instead (E-1813
	// removed tasks.tier); it decodes into nothing, deliberately — tier values
	// were dropped, not mapped onto a rating.
	Complexity *string `json:"complexity,omitempty"`
	Risk       *string `json:"risk,omitempty"`
	ParentID   *int64  `json:"parent_id,omitempty"`
	SortOrder  int     `json:"sort_order"`
	AfterID    *int64  `json:"after_id,omitempty"` // Go resolves to sort_order
}

// PlanText is the plan this event carries, whichever key spelled it. Post-E-1000
// events use `plan`; the historical ones use `text`. Plan wins when a payload
// somehow carries both, so a forward-spelled key is never overridden by a
// legacy one.
func (p TaskCreatedPayload) PlanText() string {
	if p.Plan != "" {
		return p.Plan
	}
	return p.LegacyText
}

// ratingIDs resolves the payload's rating slugs to the ids stored in
// tasks.complexity_id / tasks.risk_id; nil for an absent rating. An unknown
// slug is an error, so it never reaches the database.
func (p TaskCreatedPayload) ratingIDs() (complexityID, riskID any, err error) {
	if p.Complexity != nil {
		if complexityID, err = rating.ComplexityAxis.ColumnValue(*p.Complexity); err != nil {
			return nil, nil, fmt.Errorf("events: %w", err)
		}
	}
	if p.Risk != nil {
		if riskID, err = rating.RiskAxis.ColumnValue(*p.Risk); err != nil {
			return nil, nil, fmt.Errorf("events: %w", err)
		}
	}
	return complexityID, riskID, nil
}

// TaskImportedPayload is replay-only from E-2142 on: `task import` and
// `task import-json`, the only two commands that ever emitted task.imported, are
// gone. The kind stays live rather than retired because the events in the ledger
// CREATED TASKS, and those tasks are still there — a rebuild that skipped them
// would lose rows, which is the opposite of what a retired no-op is for.
//
// SourceFile is retained and no longer written. Nothing reads the value: E-2142
// dropped tasks.source_file, so neither the executor nor the projector puts it
// in the INSERT any more. It stays declared because historical payloads carry
// the key, and a struct that names it says so in the one place a reader of this
// type will look. Do not reintroduce a column for it.
type TaskImportedPayload struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Phase       string `json:"phase"`
	Status      string `json:"status"`
	SourceFile  string `json:"source_file,omitempty"`
	SortOrder   int    `json:"sort_order"`
	ParentID    *int64 `json:"parent_id,omitempty"`
}

type TaskStatusChangedPayload struct {
	OldStatus   string `json:"old_status"`
	NewStatus   string `json:"new_status"`
	CompletedAt string `json:"completed_at,omitempty"`
	Cascade     bool   `json:"cascade,omitempty"`

	// Outcome is the deliverable written with the transition; Reason is why the
	// task ended, written when it arrives at an abandonment status (E-1531).
	// Historical events carry the reason under `outcome` too — outcomeName
	// routes those by NewStatus, so a rebuild lands them where the migration
	// did.
	Outcome string `json:"outcome,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type TaskFieldsUpdatedPayload struct {
	Fields map[string]any `json:"fields"`
}

type TaskMovedPayload struct {
	OldParentID *int64 `json:"old_parent_id"`
	NewParentID *int64 `json:"new_parent_id"`
}

type TaskDeletedPayload struct {
	Cascade bool   `json:"cascade"`
	Title   string `json:"title"`
}

type TaskReleasedPayload struct {
	SessionID int64 `json:"session_id"`
}

type TaskClaimedPayload struct {
	SessionID int64 `json:"session_id"`
}

// TaskLandedPayload records one successful `endless worktree land`
// (E-1337). One row inserted into task_landings per event; re-landing
// (post-land bug fix on the same branch) appends a second event/row.
// The acting session is read from the envelope's actor.session_id —
// it's the session that ran the land. When empty (system actor,
// pre-bridge call), task_landings.session_id is NULL.
//
// There is no Branch field, and E-2108 removed it rather than deprecating it:
// the task branch is `task/<id>` (ED-1587), so the entity ref this event
// already carries determines the name. Events emitted before that carry a
// "branch" key in the ledger and keep it — encoding/json drops an unknown key
// on replay, so a rebuild reads them without special handling.
type TaskLandedPayload struct {
	MergeCommitSHA string `json:"merge_commit_sha"`

	// BaseBranch is the branch the work landed ON — `main` for almost every
	// project, whatever `origin/HEAD` names for the rest (E-2005). It is the
	// one branch a landing cannot derive: the task branch follows from the id,
	// but nothing says which branch a project lands into.
	//
	// omitempty and nullable downstream. A record-only backfill (E-1719) has no
	// base branch to name — the land it records happened before anything was
	// asked to remember one — and a fabricated "main" there would be a guess
	// stored as a fact.
	BaseBranch string `json:"base_branch,omitempty"`
}

// Task question payloads (E-2176).

// TaskQuestionsAskedPayload records one series of questions asked together on
// the task named by the entity ref. Callers send only the question texts;
// `event emit` fills Series and every ID under the write lock and refuses a
// payload that already carries them, so the numbers in the ledger are always
// the ones the database allocated.
type TaskQuestionsAskedPayload struct {
	Series    int64           `json:"series"`
	Questions []AskedQuestion `json:"questions"`
}

// AskedQuestion is one question within a series.
type AskedQuestion struct {
	ID       int64  `json:"id"`
	Question string `json:"question"`
}

// TaskQuestionResolvedPayload moves the question named by the entity ref out of
// its current status. The two kinds of text never mix:
//
//   - `answered` requires Answer and AnsweredBy, and refuses Reason.
//   - `withdrawn`, `invalid` and `superseded` require Reason — why the question
//     was closed without an answer — and refuse Answer and AnsweredBy.
//
// Storing a closing reason in the answer column would overload it the way
// tasks.outcome is overloaded today.
type TaskQuestionResolvedPayload struct {
	Status     string `json:"status"`
	Answer     string `json:"answer,omitempty"`
	AnsweredBy string `json:"answered_by,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Epic derivation payloads (E-1541). Recorded once per epic whose status the
// derivation rule changed. Mirrors TaskStatusChangedPayload's shape; the entity
// ref carries the epic's task id and the actor is system/epic-derivation.
type EpicStatusDerivedPayload struct {
	TaskID    int64  `json:"task_id"`
	OldStatus string `json:"old_status"`
	NewStatus string `json:"new_status"`
}

// Task dependency payloads

type TaskDepCreatedPayload struct {
	SourceID int64  `json:"source_id"`
	TargetID int64  `json:"target_id"`
	DepType  string `json:"dep_type"`
}

type TaskDepDeletedPayload struct {
	SourceID int64  `json:"source_id"`
	TargetID int64  `json:"target_id"`
	DepType  string `json:"dep_type"`
}

// Decision payloads (E-1378). status defaults to 'proposed' when omitted.
// origin_task_id and origin_session_id are 0 when unknown — Python emit
// only populates them when a triggering task / session is identifiable.

type DecisionCreatedPayload struct {
	Title           string `json:"title"`
	Description     string `json:"description,omitempty"`
	Text            string `json:"text,omitempty"`
	Status          string `json:"status,omitempty"`
	OriginTaskID    int64  `json:"origin_task_id,omitempty"`
	OriginSessionID int64  `json:"origin_session_id,omitempty"`
	Notes           string `json:"notes,omitempty"`
}

type DecisionFieldsUpdatedPayload struct {
	Fields map[string]any `json:"fields"`
}

type DecisionAcceptedPayload struct{}

type DecisionRejectedPayload struct {
	Reason string `json:"reason"`
}

// Reversal payloads (E-1864) are empty: the target status is always
// 'proposed' and the status being undone is implied by the kind. The reason
// that decision.rejected stored is preserved in the ledger entry that set it,
// so decision.unrejected does not need to carry it forward.

type DecisionUnacceptedPayload struct{}

type DecisionUnrejectedPayload struct{}

// End-state payloads (E-1920).
//
// DecisionSupersededPayload carries the replacement's id purely so the ledger
// entry is self-describing — the authoritative link is the `supersedes` row in
// decision_relations, emitted alongside as its own decision_relation.created
// event (the same split `task replace` uses: relation first, then status).
type DecisionSupersededPayload struct {
	BySupersedingID int64 `json:"by_superseding_id"`
}

// DecisionObsoletedPayload's reason is required, not decorative: an accepted
// decision governed something, and retiring it without saying what went away
// leaves exactly the unanswerable "is this still in force?" that E-1920 exists
// to close.
type DecisionObsoletedPayload struct {
	Reason string `json:"reason"`
}

// DecisionReinstatedPayload is empty for the same reason the E-1864 reversals
// are: the destination is always 'accepted', and which end state is being
// undone is recoverable from the row.
type DecisionReinstatedPayload struct{}

type DecisionDeletedPayload struct {
	Title string `json:"title"`
}

// Decision relation payloads (E-1378).
type DecisionRelationCreatedPayload struct {
	SourceDecisionID int64  `json:"source_decision_id"`
	TargetKind       string `json:"target_kind"`
	TargetID         int64  `json:"target_id"`
	RelationType     string `json:"relation_type"`
}

type DecisionRelationDeletedPayload struct {
	SourceDecisionID int64  `json:"source_decision_id"`
	TargetKind       string `json:"target_kind"`
	TargetID         int64  `json:"target_id"`
	RelationType     string `json:"relation_type"`
}

// Project payloads

type ProjectRegisteredPayload struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Path        string `json:"path"`
	GroupName   string `json:"group_name,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Language    string `json:"language,omitempty"`
}

type ProjectUpdatedPayload struct {
	Fields map[string]any `json:"fields"`
}

type ProjectRenamedPayload struct {
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
}

type ProjectUnregisteredPayload struct {
	Name string `json:"name"`
}

type ProjectPurgedPayload struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Session payloads

type SessionWorkStartedPayload struct {
	TaskID  int64  `json:"task_id"`
	Process string `json:"process,omitempty"`
}

type SessionChatStartedPayload struct {
	Process string `json:"process,omitempty"`
}

// SessionIdledPayload is intentionally empty; the entity ref carries the session ID.
type SessionIdledPayload struct{}

// SessionEndedPayload is intentionally empty.
type SessionEndedPayload struct{}

type SessionTaskCompletedPayload struct {
	TaskID int64 `json:"task_id"`
}

type SessionHiddenPayload struct{}

// Conversation payloads

type ConversationBeaconedPayload struct {
	ProcessA string `json:"process_a"`
}

type ConversationConnectedPayload struct {
	ProcessB string `json:"process_b"`
}

// ConversationClosedPayload is intentionally empty.
type ConversationClosedPayload struct{}

// Message payloads

type MessageSentPayload struct {
	ConversationID string `json:"conversation_id"`
	Sender         string `json:"sender"`
	Body           string `json:"body"`
}

type MessageDeliveredPayload struct {
	ConversationID string `json:"conversation_id"`
}

// Note payloads

type NoteCreatedPayload struct {
	NoteType string `json:"note_type"`
	Message  string `json:"message"`
}

// NoteResolvedPayload is intentionally empty; the entity ref carries the note ID.
type NoteResolvedPayload struct{}

// Session status payloads (E-1312 / E-1314)

// SessionStatusRecordedPayload carries the parsed contents of a
// <session-status> XML document, ready to be inserted as a row in
// session_statuses. All section fields are TEXT; empty string means
// "no content for this section."
//
// E-1314: collapsed the four task-disposition columns
// (resolved/pending/blocked/unverified) into a single `tasks` column.
// Disposition is derived at render time from each task's `status`
// attribute, removing redundant information. Added `summary` (structured
// per-layer implementation breakdown); `task_id` is populated by
// the Go handler from the resolved session's sessions.task_id at
// insert time — not carried in the payload.
type SessionStatusRecordedPayload struct {
	Process   string `json:"process"` // tmux pane id (or other process identifier)
	Headline  string `json:"headline"`
	Tasks     string `json:"tasks"`
	Decisions string `json:"decisions"`
	Commits   string `json:"commits"`
	Memory    string `json:"memory"`
	Summary   string `json:"summary"`
	Notes     string `json:"notes"`
}

// SessionTaskMembershipPayload carries the task list for the two session-task
// membership verbs (E-1696): `session_tasks.queued` (`session task add`) and
// `session_tasks.removed` (`session task remove`). Process is the session
// identifier — the "__session_id=N" sentinel set by the Python command, or a raw
// tmux pane id — resolved exactly as session_status.recorded's is. TaskIDs are
// display form ("E-100"); the executors strip the prefix.
//
// One struct for both kinds because the INPUT is identical — a session and a set
// of tasks — and the KIND carries the verb. Splitting it would give two types
// that must be kept byte-identical by hand, which is the drift this avoids.
type SessionTaskMembershipPayload struct {
	Process string   `json:"process"`
	TaskIDs []string `json:"task_ids"`
}
