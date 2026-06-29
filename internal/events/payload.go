package events

// Task payloads

type TaskCreatedPayload struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Text        string `json:"text,omitempty"`
	Notes       string `json:"notes,omitempty"`
	Phase       string `json:"phase"`
	Status      string `json:"status"`
	Type        string `json:"type"`
	Tier        *int   `json:"tier,omitempty"`
	ParentID    *int64 `json:"parent_id,omitempty"`
	SortOrder   int    `json:"sort_order"`
	AfterID     *int64 `json:"after_id,omitempty"` // Go resolves to sort_order
}

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
	Outcome     string `json:"outcome,omitempty"`
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

type TaskBulkClearedPayload struct {
	SourceFile string `json:"source_file,omitempty"`
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
type TaskLandedPayload struct {
	Branch         string `json:"branch"`
	MergeCommitSHA string `json:"merge_commit_sha"`
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
	SourceID int64 `json:"source_id"`
	TargetID int64 `json:"target_id"`
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

// Curated next-list payloads (E-1421)

// ProjectNextRevisedPayload is the full new state of a project's curated
// "next" list — revise is a full rewrite. Order of lanes and items is
// intrinsic in the arrays. A pending_triage field, if present, is ignored:
// the pending bucket is hook-managed and untouched by revise (E-1421).
type ProjectNextRevisedPayload struct {
	Lanes []ProjectNextLanePayload `json:"lanes"`
}

type ProjectNextLanePayload struct {
	ID        string                   `json:"id"`
	Priority  int                      `json:"priority"`
	Rationale string                   `json:"rationale"`
	Items     []ProjectNextItemPayload `json:"items"`
}

type ProjectNextItemPayload struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason"`
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

type SessionRecappedPayload struct {
	Summary string `json:"summary"`
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
// per-layer implementation breakdown); `active_task_id` is populated by
// the Go handler from the resolved session's sessions.active_task_id at
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

// SessionTasksOrderedPayload carries a replace-all per-session implementation
// order (E-1683). Process is the session identifier (the "__session_id=N"
// sentinel set by the Python command, or a raw tmux pane id) resolved the same
// way as session_status.recorded. Groups is the ordered list of parallel
// groups: group index i (0-based) maps to do_order = i+1, and every task id in
// the same inner slice shares that do_order (parallelizable). Task ids are the
// display form ("E-100"); the executor strips the prefix and validates each is
// already a session_tasks row for this session.
type SessionTasksOrderedPayload struct {
	Process string     `json:"process"`
	Groups  [][]string `json:"groups"`
}
