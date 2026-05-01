package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
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
	TaskID int64 `json:"task_id,omitempty"` // for task.created/imported
}

// PreAllocateTaskID acquires a write lock via BEGIN IMMEDIATE, reads the next
// available task ID, and returns it along with functions to finish the transaction.
//
// Usage:
//  1. Call PreAllocateTaskID() to get the ID and lock the DB
//  2. Write the event to the segment file using the returned ID
//  3. Call execAndCommit(evt) to run the SQL mutation and release the lock
//  4. If anything fails before step 3, call rollback() to release the lock
func PreAllocateTaskID() (id int64, execAndCommit func(*Event) (*ExecuteResult, error), rollback func(), err error) {
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

	doExecAndCommit := func(evt *Event) (*ExecuteResult, error) {
		result, err := dispatch(db, evt)
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

// Execute processes an event: runs the corresponding SQL mutation.
// Used for non-create events where ID pre-allocation is not needed.
func Execute(evt *Event) (*ExecuteResult, error) {
	db, err := monitor.DB()
	if err != nil {
		return nil, fmt.Errorf("events: db connection: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("events: begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := dispatch(tx, evt)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("events: commit: %w", err)
	}
	return result, nil
}

func dispatch(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	switch evt.Kind {
	case KindTaskCreated:
		return execTaskCreated(db, evt)
	case KindTaskImported:
		return execTaskImported(db, evt)
	case KindTaskStatusChanged:
		return execTaskStatusChanged(db, evt)
	case KindTaskFieldsUpdated:
		return execTaskFieldsUpdated(db, evt)
	case KindTaskMoved:
		return execTaskMoved(db, evt)
	case KindTaskDeleted:
		return execTaskDeleted(db, evt)
	case KindTaskBulkCleared:
		return execTaskBulkCleared(db, evt)
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

func execTaskCreated(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.created payload: %w", err)
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

	_, err = db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, description, status, type, sort_order, parent_id, tier, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, projectID, p.Phase, p.Title, p.Description, p.Status, p.Type,
		sortOrder, p.ParentID, p.Tier, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert task: %w", err)
	}

	return &ExecuteResult{TaskID: taskID}, nil
}

func execTaskImported(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskImportedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.imported payload: %w", err)
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	taskID := mustParseInt64(evt.Entity.ID)
	ts := now()

	_, err = db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, description, status, source_file, sort_order, parent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'needs_plan', ?, ?, ?, ?, ?)`,
		taskID, projectID, p.Phase, p.Title, p.Description, p.SourceFile,
		p.SortOrder, p.ParentID, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert imported task: %w", err)
	}

	return &ExecuteResult{TaskID: taskID}, nil
}

func execTaskStatusChanged(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskStatusChangedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.status_changed payload: %w", err)
	}

	taskID := evt.Entity.ID

	var completedAt *string
	tier := 0
	if p.NewStatus == "confirmed" {
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

	return &ExecuteResult{}, nil
}

func execTaskFieldsUpdated(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskFieldsUpdatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.fields_updated payload: %w", err)
	}

	if len(p.Fields) == 0 {
		return &ExecuteResult{}, nil
	}

	taskID := evt.Entity.ID

	var setClauses []string
	var args []any

	allowedFields := map[string]string{
		"title": "title", "description": "description", "text": "text",
		"prompt": "prompt", "phase": "phase", "tier": "tier",
		"type": "type", "status": "status", "parent_id": "parent_id",
		"outcome": "outcome",
	}

	for field, value := range p.Fields {
		col, ok := allowedFields[field]
		if !ok {
			return nil, fmt.Errorf("events: unknown field %q in task.fields_updated", field)
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, value)
	}

	if status, ok := p.Fields["status"]; ok {
		statusStr := fmt.Sprintf("%v", status)
		terminalStatuses := map[string]bool{
			"verify": true, "confirmed": true, "assumed": true,
			"declined": true, "obsolete": true,
		}
		if terminalStatuses[statusStr] {
			if _, tierSet := p.Fields["tier"]; !tierSet {
				setClauses = append(setClauses, "tier = ?")
				args = append(args, 0)
			}
		}
		if statusStr == "confirmed" {
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

	return &ExecuteResult{}, nil
}

func execTaskMoved(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskMovedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.moved payload: %w", err)
	}

	taskID := evt.Entity.ID

	if p.NewParentID != nil {
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

	return &ExecuteResult{}, nil
}

func execTaskDeleted(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.deleted payload: %w", err)
	}

	taskID := evt.Entity.ID

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
