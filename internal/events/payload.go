package events

// Task payloads

type TaskCreatedPayload struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
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
