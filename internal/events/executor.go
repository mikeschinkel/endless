package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// ExecuteResult holds the output of a successful execution.
type ExecuteResult struct {
	TaskID int64 `json:"task_id,omitempty"` // for task.created/imported
}

// Execute processes an event: runs the corresponding SQL mutation within a transaction.
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

func dispatch(tx *sql.Tx, evt *Event) (*ExecuteResult, error) {
	switch evt.Kind {
	case KindTaskCreated:
		return execTaskCreated(tx, evt)
	case KindTaskImported:
		return execTaskImported(tx, evt)
	case KindTaskStatusChanged:
		return execTaskStatusChanged(tx, evt)
	case KindTaskFieldsUpdated:
		return execTaskFieldsUpdated(tx, evt)
	case KindTaskMoved:
		return execTaskMoved(tx, evt)
	case KindTaskDeleted:
		return execTaskDeleted(tx, evt)
	case KindTaskBulkCleared:
		return execTaskBulkCleared(tx, evt)
	default:
		return nil, fmt.Errorf("events: executor does not handle kind %q", evt.Kind)
	}
}

// resolveProjectID looks up the integer project ID from the project name.
func resolveProjectID(tx *sql.Tx, name string) (int64, error) {
	var id int64
	err := tx.QueryRow("SELECT id FROM projects WHERE name = ?", name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("events: project %q not found: %w", name, err)
	}
	return id, nil
}

func now() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05")
}

func execTaskCreated(tx *sql.Tx, evt *Event) (*ExecuteResult, error) {
	var p TaskCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.created payload: %w", err)
	}

	projectID, err := resolveProjectID(tx, evt.Project)
	if err != nil {
		return nil, err
	}

	// Calculate sort_order
	sortOrder := p.SortOrder
	if p.AfterID != nil {
		var afterSort int
		err := tx.QueryRow("SELECT sort_order FROM tasks WHERE id = ?", *p.AfterID).Scan(&afterSort)
		if err != nil {
			return nil, fmt.Errorf("events: after task %d not found: %w", *p.AfterID, err)
		}
		sortOrder = afterSort + 5
	} else if sortOrder == 0 {
		var maxSort sql.NullInt64
		tx.QueryRow("SELECT MAX(sort_order) FROM tasks WHERE project_id = ? AND phase = ?",
			projectID, p.Phase).Scan(&maxSort)
		if maxSort.Valid {
			sortOrder = int(maxSort.Int64) + 10
		} else {
			sortOrder = 10
		}
	}

	ts := now()
	result, err := tx.Exec(
		`INSERT INTO tasks (project_id, phase, title, description, status, type, sort_order, parent_id, tier, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projectID, p.Phase, p.Title, p.Description, p.Status, p.Type,
		sortOrder, p.ParentID, p.Tier, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert task: %w", err)
	}

	taskID, _ := result.LastInsertId()
	return &ExecuteResult{TaskID: taskID}, nil
}

func execTaskImported(tx *sql.Tx, evt *Event) (*ExecuteResult, error) {
	var p TaskImportedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.imported payload: %w", err)
	}

	projectID, err := resolveProjectID(tx, evt.Project)
	if err != nil {
		return nil, err
	}

	ts := now()
	result, err := tx.Exec(
		`INSERT INTO tasks (project_id, phase, title, description, status, source_file, sort_order, parent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'needs_plan', ?, ?, ?, ?, ?)`,
		projectID, p.Phase, p.Title, p.Description, p.SourceFile,
		p.SortOrder, p.ParentID, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert imported task: %w", err)
	}

	taskID, _ := result.LastInsertId()
	return &ExecuteResult{TaskID: taskID}, nil
}

func execTaskStatusChanged(tx *sql.Tx, evt *Event) (*ExecuteResult, error) {
	var p TaskStatusChangedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.status_changed payload: %w", err)
	}

	taskID := evt.Entity.ID

	// Determine completed_at and tier
	var completedAt *string
	tier := 0 // terminal statuses clear tier
	if p.NewStatus == "confirmed" {
		ts := now()
		completedAt = &ts
	}

	if p.Cascade {
		// Recursive CTE for cascade
		_, err := tx.Exec(
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
		_, err := tx.Exec(
			"UPDATE tasks SET status = ?, completed_at = ?, tier = ? WHERE id = ?",
			p.NewStatus, completedAt, tier, taskID,
		)
		if err != nil {
			return nil, fmt.Errorf("events: status change: %w", err)
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskFieldsUpdated(tx *sql.Tx, evt *Event) (*ExecuteResult, error) {
	var p TaskFieldsUpdatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.fields_updated payload: %w", err)
	}

	if len(p.Fields) == 0 {
		return &ExecuteResult{}, nil
	}

	taskID := evt.Entity.ID

	// Build dynamic UPDATE
	var setClauses []string
	var args []any

	// Allowed fields and their column names
	allowedFields := map[string]string{
		"title":       "title",
		"description": "description",
		"text":        "text",
		"prompt":      "prompt",
		"phase":       "phase",
		"tier":        "tier",
		"type":        "type",
		"status":      "status",
		"parent_id":   "parent_id",
	}

	for field, value := range p.Fields {
		col, ok := allowedFields[field]
		if !ok {
			return nil, fmt.Errorf("events: unknown field %q in task.fields_updated", field)
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, value)
	}

	// Handle status-related side effects
	if status, ok := p.Fields["status"]; ok {
		statusStr := fmt.Sprintf("%v", status)
		terminalStatuses := map[string]bool{
			"verify": true, "confirmed": true, "assumed": true,
			"declined": true, "obsolete": true,
		}
		if terminalStatuses[statusStr] {
			// Clear tier on terminal status unless tier is explicitly being set
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

	if _, err := tx.Exec(query, args...); err != nil {
		return nil, fmt.Errorf("events: update task fields: %w", err)
	}

	return &ExecuteResult{}, nil
}

func execTaskMoved(tx *sql.Tx, evt *Event) (*ExecuteResult, error) {
	var p TaskMovedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.moved payload: %w", err)
	}

	taskID := evt.Entity.ID

	// Circular reference check: walk up from new parent to ensure taskID is not an ancestor
	if p.NewParentID != nil {
		current := *p.NewParentID
		for {
			var parentID sql.NullInt64
			err := tx.QueryRow("SELECT parent_id FROM tasks WHERE id = ?", current).Scan(&parentID)
			if err != nil {
				break
			}
			if !parentID.Valid {
				break // reached root
			}
			if parentID.Int64 == mustParseInt64(taskID) {
				return nil, fmt.Errorf("events: circular reference: task %s is an ancestor of target parent %d", taskID, *p.NewParentID)
			}
			current = parentID.Int64
		}
	}

	if _, err := tx.Exec("UPDATE tasks SET parent_id = ? WHERE id = ?",
		p.NewParentID, taskID); err != nil {
		return nil, fmt.Errorf("events: move task: %w", err)
	}

	return &ExecuteResult{}, nil
}

func execTaskDeleted(tx *sql.Tx, evt *Event) (*ExecuteResult, error) {
	var p TaskDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.deleted payload: %w", err)
	}

	taskID := evt.Entity.ID

	if p.Cascade {
		// Recursive delete
		if _, err := tx.Exec(
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
		// Reparent children first
		if _, err := tx.Exec(
			"UPDATE tasks SET parent_id = NULL WHERE parent_id = ?", taskID,
		); err != nil {
			return nil, fmt.Errorf("events: reparent children: %w", err)
		}
		if _, err := tx.Exec("DELETE FROM tasks WHERE id = ?", taskID); err != nil {
			return nil, fmt.Errorf("events: delete task: %w", err)
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskBulkCleared(tx *sql.Tx, evt *Event) (*ExecuteResult, error) {
	var p TaskBulkClearedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.bulk_cleared payload: %w", err)
	}

	projectID, err := resolveProjectID(tx, evt.Project)
	if err != nil {
		return nil, err
	}

	// NULL out parent_ids first to avoid FK issues
	if _, err := tx.Exec(
		`UPDATE tasks SET parent_id = NULL WHERE parent_id IN (
			SELECT id FROM tasks WHERE project_id = ? AND source_file = ?
		)`, projectID, p.SourceFile,
	); err != nil {
		return nil, fmt.Errorf("events: null parents for bulk clear: %w", err)
	}

	if _, err := tx.Exec(
		"DELETE FROM tasks WHERE project_id = ? AND source_file = ?",
		projectID, p.SourceFile,
	); err != nil {
		return nil, fmt.Errorf("events: bulk clear: %w", err)
	}

	return &ExecuteResult{}, nil
}

// joinStrings joins strings with a separator (avoids importing strings package).
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
