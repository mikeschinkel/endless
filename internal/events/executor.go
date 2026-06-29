package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// dbQuerier is satisfied by both *sql.Tx and *sql.DB, allowing the executor
// functions to work in both transactional and raw-connection contexts.
type dbQuerier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// ExecuteResult holds the output of a successful execution.
type ExecuteResult struct {
	TaskID          int64              `json:"task_id,omitempty"`           // for task.created/imported
	DecisionID      int64              `json:"decision_id,omitempty"`       // for decision.created (E-1378)
	SessionStatusID int64              `json:"session_status_id,omitempty"` // for session_status.recorded (E-1312)
	Skipped         bool               `json:"skipped,omitempty"`           // dedup-skip path (no row written)
	Markdown        string             `json:"markdown,omitempty"`          // rendered output for chat display
	ProjectNext     *ProjectNextResult `json:"-"`                           // for project_next.revised (E-1436)
}

// PreAllocateTaskID acquires a write lock via BEGIN IMMEDIATE, reads the next
// available task ID, and returns it along with functions to finish the transaction.
//
// Usage:
//  1. Call PreAllocateTaskID() to get the ID and lock the DB
//  2. Write the event to the segment file using the returned ID
//  3. Call execAndCommit(evt) to run the SQL mutation and release the lock
//  4. If anything fails before step 3, call rollback() to release the lock
func PreAllocateTaskID() (id int64, execAndCommit func(*Event, DerivedEmitter) (*ExecuteResult, error), rollback func(), err error) {
	db, err := monitor.DB()
	if err != nil {
		return 0, nil, nil, fmt.Errorf("events: db connection: %w", err)
	}

	// BEGIN IMMEDIATE acquires write lock, blocking other writers
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return 0, nil, nil, fmt.Errorf("events: begin immediate: %w", err)
	}

	err = db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM tasks").Scan(&id)
	if err != nil {
		db.Exec("ROLLBACK")
		return 0, nil, nil, fmt.Errorf("events: pre-allocate task id: %w", err)
	}

	doRollback := func() {
		db.Exec("ROLLBACK")
	}

	doExecAndCommit := func(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
		result, err := dispatch(db, evt, emit)
		if err != nil {
			db.Exec("ROLLBACK")
			return nil, err
		}
		if _, err := db.Exec("COMMIT"); err != nil {
			return nil, fmt.Errorf("events: commit: %w", err)
		}
		return result, nil
	}

	return id, doExecAndCommit, doRollback, nil
}

// PreAllocateDecisionID is the decision-table parallel of PreAllocateTaskID.
// Decisions use their own auto-increment column (E-1378); the ID space is
// independent of tasks (display prefix ED- disambiguates).
//
// Usage mirrors PreAllocateTaskID — see that docstring.
func PreAllocateDecisionID() (id int64, execAndCommit func(*Event, DerivedEmitter) (*ExecuteResult, error), rollback func(), err error) {
	db, err := monitor.DB()
	if err != nil {
		return 0, nil, nil, fmt.Errorf("events: db connection: %w", err)
	}

	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return 0, nil, nil, fmt.Errorf("events: begin immediate: %w", err)
	}

	err = db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM decisions").Scan(&id)
	if err != nil {
		db.Exec("ROLLBACK")
		return 0, nil, nil, fmt.Errorf("events: pre-allocate decision id: %w", err)
	}

	doRollback := func() {
		db.Exec("ROLLBACK")
	}

	doExecAndCommit := func(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
		result, err := dispatch(db, evt, emit)
		if err != nil {
			db.Exec("ROLLBACK")
			return nil, err
		}
		if _, err := db.Exec("COMMIT"); err != nil {
			return nil, fmt.Errorf("events: commit: %w", err)
		}
		return result, nil
	}

	return id, doExecAndCommit, doRollback, nil
}

// BeginImmediate acquires a write lock via BEGIN IMMEDIATE and returns
// functions to finish the transaction. Unlike PreAllocateTaskID it reads
// nothing up front — it exists so a multi-row rewrite (E-1436 revise) can
// take the write lock BEFORE the events-authoritative ledger append, the
// same ordering as the create path. A losing concurrent writer then fails
// at BEGIN (SQLITE_BUSY) without leaving an orphan ledger line.
//
// Usage:
//  1. Call BeginImmediate() to lock the DB
//  2. Append the event to the segment file
//  3. Call execAndCommit(evt) to run the SQL mutation and release the lock
//  4. If anything fails before step 3, call rollback() to release the lock
func BeginImmediate() (execAndCommit func(*Event, DerivedEmitter) (*ExecuteResult, error), rollback func(), err error) {
	db, err := monitor.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("events: db connection: %w", err)
	}

	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return nil, nil, fmt.Errorf("events: begin immediate: %w", err)
	}

	doRollback := func() {
		db.Exec("ROLLBACK")
	}

	doExecAndCommit := func(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
		result, err := dispatch(db, evt, emit)
		if err != nil {
			db.Exec("ROLLBACK")
			return nil, err
		}
		if _, err := db.Exec("COMMIT"); err != nil {
			return nil, fmt.Errorf("events: commit: %w", err)
		}
		return result, nil
	}

	return doExecAndCommit, doRollback, nil
}

// Execute processes an event: runs the corresponding SQL mutation.
// Used for non-create events where ID pre-allocation is not needed. emit is the
// epic-derivation ledger emitter (E-1541); it is nil for events that cannot
// trigger derivation and from callers that record no derived events (tests).
func Execute(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	db, err := monitor.DB()
	if err != nil {
		return nil, fmt.Errorf("events: db connection: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("events: begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := dispatch(tx, evt, emit)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("events: commit: %w", err)
	}
	return result, nil
}

// dispatch routes an event to its executor. emit (E-1541) is threaded to the
// six task mutations that can change an epic's derived status; the remaining
// executors ignore derivation.
func dispatch(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	switch evt.Kind {
	case KindTaskCreated:
		return execTaskCreated(db, evt, emit)
	case KindTaskImported:
		return execTaskImported(db, evt, emit)
	case KindTaskStatusChanged:
		return execTaskStatusChanged(db, evt, emit)
	case KindTaskFieldsUpdated:
		return execTaskFieldsUpdated(db, evt, emit)
	case KindTaskMoved:
		return execTaskMoved(db, evt, emit)
	case KindTaskDeleted:
		return execTaskDeleted(db, evt, emit)
	case KindTaskBulkCleared:
		return execTaskBulkCleared(db, evt)
	case KindTaskReleased:
		return execTaskReleased(db, evt)
	case KindTaskClaimed:
		return execTaskClaimed(db, evt)
	case KindTaskLanded:
		return execTaskLanded(db, evt)
	case KindSessionStatusRecorded:
		return execSessionStatusRecorded(db, evt)
	case KindSessionTasksOrdered:
		return execSessionTasksOrdered(db, evt)
	case KindProjectNextRevised:
		return execProjectNextRevised(db, evt)
	case KindDecisionCreated:
		return execDecisionCreated(db, evt)
	case KindDecisionFieldsUpdated:
		return execDecisionFieldsUpdated(db, evt)
	case KindDecisionAccepted:
		return execDecisionAccepted(db, evt)
	case KindDecisionRejected:
		return execDecisionRejected(db, evt)
	case KindDecisionDeleted:
		return execDecisionDeleted(db, evt)
	case KindDecisionRelationCreated:
		return execDecisionRelationCreated(db, evt)
	case KindDecisionRelationDeleted:
		return execDecisionRelationDeleted(db, evt)
	default:
		return nil, fmt.Errorf("events: executor does not handle kind %q", evt.Kind)
	}
}

func resolveProjectID(db dbQuerier, name string) (int64, error) {
	var id int64
	err := db.QueryRow("SELECT id FROM projects WHERE name = ?", name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("events: project %q not found: %w", name, err)
	}
	return id, nil
}

func now() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05")
}

func execTaskCreated(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.created payload: %w", err)
	}
	if err := ValidatePhase(p.Phase); err != nil {
		return nil, err
	}
	if err := ValidateMaybeParentless(p.Phase, p.ParentID); err != nil {
		return nil, err
	}
	typeID, err := tasktype.Parse(p.Type)
	if err != nil {
		return nil, err
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	sortOrder := p.SortOrder
	if p.AfterID != nil {
		var afterSort int
		err := db.QueryRow("SELECT sort_order FROM tasks WHERE id = ?", *p.AfterID).Scan(&afterSort)
		if err != nil {
			return nil, fmt.Errorf("events: after task %d not found: %w", *p.AfterID, err)
		}
		sortOrder = afterSort + 5
	} else if sortOrder == 0 {
		var maxSort sql.NullInt64
		db.QueryRow("SELECT MAX(sort_order) FROM tasks WHERE project_id = ? AND phase = ?",
			projectID, p.Phase).Scan(&maxSort)
		if maxSort.Valid {
			sortOrder = int(maxSort.Int64) + 10
		} else {
			sortOrder = 10
		}
	}

	// Use explicit ID from entity ref (pre-allocated by caller)
	taskID := mustParseInt64(evt.Entity.ID)
	ts := now()

	// Attaching a non-empty plan at creation auto-promotes the task to
	// `ready`. Mirrors the behavior of task.fields_updated when --text is
	// supplied. The promotion only fires when status was the default
	// `unplanned` — an explicit override (e.g. a tier-1 task created at
	// `ready` already, or any non-default status) is preserved.
	status := p.Status
	if status == "unplanned" && strings.TrimSpace(p.Text) != "" {
		status = "ready"
	}

	var notes any
	if p.Notes != "" {
		notes = p.Notes
	}
	_, err = db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, description, text, notes, status, type_id, sort_order, parent_id, tier, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, projectID, p.Phase, p.Title, p.Description, p.Text, notes, status, int(typeID),
		sortOrder, p.ParentID, p.Tier, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert task: %w", err)
	}

	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, taskID); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	if p.Phase == "urgent" {
		if err := autoAddUrgentPending(db, evt, taskID); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	// E-1541: a new child can change its parent epic's derived status.
	if p.ParentID != nil {
		if err := recomputeEpicStatus(db, emit, *p.ParentID); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{TaskID: taskID}, nil
}

func execTaskImported(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskImportedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.imported payload: %w", err)
	}
	if err := ValidatePhase(p.Phase); err != nil {
		return nil, err
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	taskID := mustParseInt64(evt.Entity.ID)
	ts := now()

	_, err = db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, description, status, source_file, sort_order, parent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'unplanned', ?, ?, ?, ?, ?)`,
		taskID, projectID, p.Phase, p.Title, p.Description, p.SourceFile,
		p.SortOrder, p.ParentID, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert imported task: %w", err)
	}

	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, taskID); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	// E-1541: an imported child can change its parent epic's derived status.
	if p.ParentID != nil {
		if err := recomputeEpicStatus(db, emit, *p.ParentID); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{TaskID: taskID}, nil
}

func execTaskStatusChanged(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskStatusChangedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.status_changed payload: %w", err)
	}

	taskID := evt.Entity.ID

	var completedAt *string
	tier := 0
	if p.NewStatus == "confirmed" || p.NewStatus == "completed" {
		ts := now()
		completedAt = &ts
	}

	if p.Cascade {
		_, err := db.Exec(
			`WITH RECURSIVE tree(id) AS (
				SELECT id FROM tasks WHERE id = ?
				UNION ALL
				SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id
			) UPDATE tasks SET status = ?, completed_at = ?, tier = ?
			WHERE id IN (SELECT id FROM tree) AND status != ?`,
			taskID, p.NewStatus, completedAt, tier, p.NewStatus,
		)
		if err != nil {
			return nil, fmt.Errorf("events: cascade status change: %w", err)
		}
	} else {
		_, err := db.Exec(
			"UPDATE tasks SET status = ?, completed_at = ?, tier = ? WHERE id = ?",
			p.NewStatus, completedAt, tier, taskID,
		)
		if err != nil {
			return nil, fmt.Errorf("events: status change: %w", err)
		}
	}

	if p.Outcome != "" {
		if _, err := db.Exec(
			"UPDATE tasks SET outcome = ? WHERE id = ?",
			p.Outcome, taskID,
		); err != nil {
			return nil, fmt.Errorf("events: set outcome: %w", err)
		}
	}

	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID)); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	// E-1541: the changed task's status may alter its parent epic chain.
	// Recompute from the parent up; the changed task itself is never
	// re-derived, so an explicit set (e.g. a cascade confirm) is preserved.
	if parentID, ok, err := taskParentID(db, mustParseInt64(taskID)); err != nil {
		return nil, err
	} else if ok {
		if err := recomputeEpicStatus(db, emit, parentID); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskFieldsUpdated(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskFieldsUpdatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.fields_updated payload: %w", err)
	}

	if len(p.Fields) == 0 {
		return &ExecuteResult{}, nil
	}

	taskID := evt.Entity.ID

	// E-1541: a status or parent_id change can alter a parent epic's derived
	// status. Capture the old parent before the update so a re-parent
	// recomputes both the old and new chains.
	_, statusChanging := p.Fields["status"]
	_, parentChanging := p.Fields["parent_id"]
	var oldParentID int64
	var hasOldParent bool
	if parentChanging {
		var perr error
		oldParentID, hasOldParent, perr = taskParentID(db, mustParseInt64(taskID))
		if perr != nil {
			return nil, perr
		}
	}

	var setClauses []string
	var args []any

	allowedFields := map[string]string{
		"title": "title", "description": "description", "text": "text",
		"notes": "notes",
		"phase": "phase", "tier": "tier",
		"type": "type_id", "status": "status", "parent_id": "parent_id",
		"outcome": "outcome", "analysis": "analysis",
	}

	for field, value := range p.Fields {
		col, ok := allowedFields[field]
		if !ok {
			return nil, fmt.Errorf("events: unknown field %q in task.fields_updated", field)
		}
		if field == "phase" {
			phaseStr, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("events: phase field must be string, got %T", value)
			}
			if err := ValidatePhase(phaseStr); err != nil {
				return nil, err
			}
		}
		if field == "type" {
			typeStr, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("events: type field must be string, got %T", value)
			}
			tt, err := tasktype.Parse(typeStr)
			if err != nil {
				return nil, err
			}
			value = int(tt)
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, value)
	}

	// Reject a task that would be both maybe-phase and parented. Only
	// evaluate when this update touches phase or parent_id — an unrelated
	// edit must not be blocked just because the row already violates. The
	// incoming field wins; the absent side is read from the current row.
	// parent_id arrives as a JSON number (float64) or null; null/0 → root.
	_, phaseSet := p.Fields["phase"]
	rawParent, parentSet := p.Fields["parent_id"]
	if phaseSet || parentSet {
		var curPhase string
		var curParent sql.NullInt64
		if err := db.QueryRow("SELECT phase, parent_id FROM tasks WHERE id = ?",
			taskID).Scan(&curPhase, &curParent); err != nil {
			return nil, fmt.Errorf("events: load task for maybe-parent check: %w", err)
		}
		effPhase := curPhase
		if v, ok := p.Fields["phase"].(string); ok {
			effPhase = v
		}
		var effParent *int64
		if parentSet {
			if v, ok := rawParent.(float64); ok && v > 0 {
				id := int64(v)
				effParent = &id
			}
		} else if curParent.Valid {
			effParent = &curParent.Int64
		}
		if err := ValidateMaybeParentless(effPhase, effParent); err != nil {
			return nil, err
		}
	}

	// Attaching a non-empty plan (--text) auto-promotes a `unplanned`
	// task to `ready`. Only fires when the same update does not already
	// set status explicitly (caller wins).
	if textVal, hasText := p.Fields["text"]; hasText {
		if _, statusSet := p.Fields["status"]; !statusSet {
			textStr, _ := textVal.(string)
			if strings.TrimSpace(textStr) != "" {
				var currentStatus string
				if err := db.QueryRow("SELECT status FROM tasks WHERE id = ?",
					taskID).Scan(&currentStatus); err == nil {
					if currentStatus == "unplanned" {
						setClauses = append(setClauses, "status = ?")
						args = append(args, "ready")
					}
				}
			}
		}
	}

	if status, ok := p.Fields["status"]; ok {
		statusStr := fmt.Sprintf("%v", status)
		terminalStatuses := map[string]bool{
			"unverified": true, "confirmed": true, "assumed": true,
			"completed": true, "declined": true, "obsolete": true,
		}
		if terminalStatuses[statusStr] {
			if _, tierSet := p.Fields["tier"]; !tierSet {
				setClauses = append(setClauses, "tier = ?")
				args = append(args, 0)
			}
		}
		if statusStr == "confirmed" || statusStr == "completed" {
			setClauses = append(setClauses, "completed_at = ?")
			args = append(args, now())
		} else {
			setClauses = append(setClauses, "completed_at = NULL")
		}
	}

	args = append(args, taskID)
	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?",
		joinStrings(setClauses, ", "))

	if _, err := db.Exec(query, args...); err != nil {
		return nil, fmt.Errorf("events: update task fields: %w", err)
	}

	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID)); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	if phaseVal, hasPhase := p.Fields["phase"]; hasPhase {
		if phaseStr, ok := phaseVal.(string); ok && phaseStr == "urgent" {
			if err := autoAddUrgentPending(db, evt, mustParseInt64(evt.Entity.ID)); err != nil {
				return nil, fmt.Errorf("events: %w", err)
			}
		}
	}

	// E-1541: recompute the affected epic chains. The current (post-update)
	// parent covers a status change and a re-parent's new chain; oldParentID
	// covers the chain the task left.
	if statusChanging || parentChanging {
		var parentIDs []int64
		if pid, ok, err := taskParentID(db, mustParseInt64(taskID)); err != nil {
			return nil, err
		} else if ok {
			parentIDs = append(parentIDs, pid)
		}
		if parentChanging && hasOldParent {
			parentIDs = append(parentIDs, oldParentID)
		}
		if len(parentIDs) > 0 {
			if err := recomputeEpicStatus(db, emit, parentIDs...); err != nil {
				return nil, err
			}
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskMoved(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskMovedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.moved payload: %w", err)
	}

	taskID := evt.Entity.ID

	if p.NewParentID != nil {
		// Reject moving a maybe-phase task under a parent (root moves are fine).
		var curPhase string
		if err := db.QueryRow("SELECT phase FROM tasks WHERE id = ?", taskID).Scan(&curPhase); err != nil {
			return nil, fmt.Errorf("events: load task for maybe-parent check: %w", err)
		}
		if err := ValidateMaybeParentless(curPhase, p.NewParentID); err != nil {
			return nil, err
		}

		current := *p.NewParentID
		for {
			var parentID sql.NullInt64
			err := db.QueryRow("SELECT parent_id FROM tasks WHERE id = ?", current).Scan(&parentID)
			if err != nil {
				break
			}
			if !parentID.Valid {
				break
			}
			if parentID.Int64 == mustParseInt64(taskID) {
				return nil, fmt.Errorf("events: circular reference: task %s is an ancestor of target parent %d", taskID, *p.NewParentID)
			}
			current = parentID.Int64
		}
	}

	if _, err := db.Exec("UPDATE tasks SET parent_id = ? WHERE id = ?",
		p.NewParentID, taskID); err != nil {
		return nil, fmt.Errorf("events: move task: %w", err)
	}

	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID)); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	// E-1541: both the old and new parent chains can change derived status.
	var parentIDs []int64
	if p.NewParentID != nil {
		parentIDs = append(parentIDs, *p.NewParentID)
	}
	if p.OldParentID != nil {
		parentIDs = append(parentIDs, *p.OldParentID)
	}
	if len(parentIDs) > 0 {
		if err := recomputeEpicStatus(db, emit, parentIDs...); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskDeleted(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.deleted payload: %w", err)
	}

	taskID := evt.Entity.ID

	// E-1541: capture the parent before the row is gone so its epic chain can
	// be recomputed after the deletion (the task left the parent's child set).
	parentID, hasParent, err := taskParentID(db, mustParseInt64(taskID))
	if err != nil {
		return nil, err
	}

	if p.Cascade {
		if _, err := db.Exec(
			`WITH RECURSIVE tree(id) AS (
				SELECT id FROM tasks WHERE id = ?
				UNION ALL
				SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id
			) DELETE FROM tasks WHERE id IN (SELECT id FROM tree)`,
			taskID,
		); err != nil {
			return nil, fmt.Errorf("events: cascade delete: %w", err)
		}
	} else {
		db.Exec("UPDATE tasks SET parent_id = NULL WHERE parent_id = ?", taskID)
		if _, err := db.Exec("DELETE FROM tasks WHERE id = ?", taskID); err != nil {
			return nil, fmt.Errorf("events: delete task: %w", err)
		}
	}

	// Record only the primary entity even on cascade — cascaded child
	// deletes are derived effects, not direct touches by the session.
	// session_tasks has no FK on task_id, so the row survives the delete.
	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID)); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	if hasParent {
		if err := recomputeEpicStatus(db, emit, parentID); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskBulkCleared(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskBulkClearedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.bulk_cleared payload: %w", err)
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	// Enumerate target task IDs BEFORE the delete so session_tasks can
	// record per-cleared-task touches. session_tasks has no FK on
	// task_id, so the rows survive the subsequent delete.
	if shouldRecordSessionTouch(evt) {
		rows, err := db.Query(
			"SELECT id FROM tasks WHERE project_id = ? AND source_file = ?",
			projectID, p.SourceFile,
		)
		if err != nil {
			return nil, fmt.Errorf("events: enumerate bulk_cleared tasks: %w", err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("events: scan bulk_cleared id: %w", err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			if err := upsertSessionTask(db, evt.Actor.SessionID, id); err != nil {
				return nil, fmt.Errorf("events: %w", err)
			}
		}
	}

	db.Exec(
		`UPDATE tasks SET parent_id = NULL WHERE parent_id IN (
			SELECT id FROM tasks WHERE project_id = ? AND source_file = ?
		)`, projectID, p.SourceFile,
	)
	if _, err := db.Exec(
		"DELETE FROM tasks WHERE project_id = ? AND source_file = ?",
		projectID, p.SourceFile,
	); err != nil {
		return nil, fmt.Errorf("events: bulk clear: %w", err)
	}

	return &ExecuteResult{}, nil
}

func execTaskClaimed(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskClaimedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.claimed payload: %w", err)
	}
	taskID := evt.Entity.ID
	// Also resolve and store active_epic_id: the nearest type='epic' ancestor of
	// the claimed task (the task itself if it is an epic, NULL if no epic
	// ancestor). E-1571 deferred this coordinator-side write; without it an
	// interactive session's active_epic_id stays NULL, so the status-line epic
	// prefix and `endless agents` auto-resolve never fire. The correlated
	// subquery mirrors monitor.nearestEpicAncestor's recursive CTE, keeping the
	// write a single atomic statement rather than exporting the helper. A NULL
	// result also clears any stale epic id left from a prior claim.
	if _, err := db.Exec(
		`UPDATE sessions
		    SET active_task_id = ?,
		        active_epic_id = (
		          WITH RECURSIVE ancestry(id, parent_id, type_id, depth) AS (
		            SELECT id, parent_id, type_id, 0 FROM tasks WHERE id = ?
		            UNION ALL
		            SELECT t.id, t.parent_id, t.type_id, a.depth + 1
		              FROM tasks t JOIN ancestry a ON t.id = a.parent_id
		          )
		          SELECT a.id FROM ancestry a
		            JOIN task_types tt ON tt.id = a.type_id
		           WHERE tt.slug = 'epic'
		           ORDER BY a.depth LIMIT 1
		        )
		  WHERE id = ?`,
		taskID, taskID, p.SessionID,
	); err != nil {
		return nil, fmt.Errorf("events: claim task: %w", err)
	}
	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID)); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}
	return &ExecuteResult{}, nil
}

func execTaskLanded(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskLandedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.landed payload: %w", err)
	}
	taskID := mustParseInt64(evt.Entity.ID)
	var sessionID any
	if evt.Actor.SessionID != "" {
		sessionID = mustParseInt64(evt.Actor.SessionID)
	}
	if _, err := db.Exec(
		`INSERT INTO task_landings (task_id, session_id, branch, merge_commit_sha, landed_at)
		 VALUES (?, ?, ?, ?, ?)`,
		taskID, sessionID, p.Branch, p.MergeCommitSHA, now(),
	); err != nil {
		return nil, fmt.Errorf("events: insert task_landing: %w", err)
	}
	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, taskID); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}
	return &ExecuteResult{}, nil
}

func execTaskReleased(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskReleasedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.released payload: %w", err)
	}
	taskID := evt.Entity.ID
	// Clear active_epic_id alongside active_task_id so a released coordinator
	// session does not keep a stale epic prefix / auto-resolve target.
	if _, err := db.Exec(
		"UPDATE sessions SET active_task_id = NULL, active_epic_id = NULL WHERE id = ? AND active_task_id = ?",
		p.SessionID, taskID,
	); err != nil {
		return nil, fmt.Errorf("events: release task: %w", err)
	}
	if shouldRecordSessionTouch(evt) {
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID)); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}
	return &ExecuteResult{}, nil
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}

func mustParseInt64(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}
