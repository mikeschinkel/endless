package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

// ProjectResult holds the outcome of a projection.
type ProjectResult struct {
	EventsReplayed int
	TasksCreated   int
	TasksUpdated   int
	TasksDeleted   int
	Errors         []string
}

// ProjectToTempDB replays task events into a fresh temporary SQLite database.
// Returns the path to the temp DB and the projection result.
// The caller is responsible for removing the temp DB when done.
func ProjectToTempDB(projectRoot string) (string, *ProjectResult, error) {
	schemaSQL := schema.SQL
	// Read all events
	events, err := ReadAllEvents(projectRoot)
	if err != nil {
		return "", nil, fmt.Errorf("projector: read events: %w", err)
	}

	if len(events) == 0 {
		return "", nil, fmt.Errorf("projector: no events found in %s", projectRoot)
	}

	// Create temp DB
	tempDir := filepath.Join(projectRoot, ".endless")
	tempPath := filepath.Join(tempDir, "projection-temp.db")
	os.Remove(tempPath) // clean up any previous temp

	tempDB, err := sql.Open("sqlite", tempPath)
	if err != nil {
		return "", nil, fmt.Errorf("projector: open temp db: %w", err)
	}
	defer tempDB.Close()

	// Initialize schema
	if _, err := tempDB.Exec(schemaSQL); err != nil {
		os.Remove(tempPath)
		return "", nil, fmt.Errorf("projector: init schema: %w", err)
	}
	if _, err := tempDB.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		os.Remove(tempPath)
		return "", nil, fmt.Errorf("projector: set pragmas: %w", err)
	}

	// Replay events
	result := &ProjectResult{}
	for _, evt := range events {
		if err := replayEvent(tempDB, &evt, result); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("ts=%s kind=%s: %s", evt.TS, evt.Kind, err))
		}
		result.EventsReplayed++
	}

	return tempPath, result, nil
}

func replayEvent(db *sql.DB, evt *Event, result *ProjectResult) error {
	switch evt.Kind {
	case KindTaskCreated:
		return replayTaskCreated(db, evt, result)
	case KindTaskImported:
		return replayTaskImported(db, evt, result)
	case KindTaskStatusChanged:
		return replayTaskStatusChanged(db, evt, result)
	case KindTaskFieldsUpdated:
		return replayTaskFieldsUpdated(db, evt, result)
	case KindTaskMoved:
		return replayTaskMoved(db, evt, result)
	case KindTaskDeleted:
		return replayTaskDeleted(db, evt, result)
	case KindTaskBulkCleared:
		return replayTaskBulkCleared(db, evt, result)
	default:
		// Skip non-task events silently (sessions, notes, etc.)
		return nil
	}
}

func replayTaskCreated(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p TaskCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return err
	}

	projectID, err := ensureProject(db, evt.Project)
	if err != nil {
		return err
	}

	taskID := mustParseInt64(evt.Entity.ID)

	// Calculate sort_order
	sortOrder := p.SortOrder
	if p.AfterID != nil {
		var afterSort int
		if err := db.QueryRow("SELECT sort_order FROM tasks WHERE id = ?", *p.AfterID).Scan(&afterSort); err == nil {
			sortOrder = afterSort + 5
		}
	}
	if sortOrder == 0 {
		var maxSort sql.NullInt64
		db.QueryRow("SELECT MAX(sort_order) FROM tasks WHERE project_id = ? AND phase = ?",
			projectID, p.Phase).Scan(&maxSort)
		if maxSort.Valid {
			sortOrder = int(maxSort.Int64) + 10
		} else {
			sortOrder = 10
		}
	}

	// Extract timestamp from kairos for created_at
	ts := kairosToISO(evt.TS)

	_, err = db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, description, status, type, sort_order, parent_id, tier, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, projectID, p.Phase, p.Title, p.Description, p.Status, p.Type,
		sortOrder, p.ParentID, p.Tier, ts, ts,
	)
	if err != nil {
		return fmt.Errorf("insert task %d: %w", taskID, err)
	}
	result.TasksCreated++
	return nil
}

func replayTaskImported(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p TaskImportedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return err
	}

	projectID, err := ensureProject(db, evt.Project)
	if err != nil {
		return err
	}

	taskID := mustParseInt64(evt.Entity.ID)
	ts := kairosToISO(evt.TS)

	_, err = db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, description, status, source_file, sort_order, parent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'needs_plan', ?, ?, ?, ?, ?)`,
		taskID, projectID, p.Phase, p.Title, p.Description, p.SourceFile,
		p.SortOrder, p.ParentID, ts, ts,
	)
	if err != nil {
		return fmt.Errorf("insert imported task %d: %w", taskID, err)
	}
	result.TasksCreated++
	return nil
}

func replayTaskStatusChanged(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p TaskStatusChangedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return err
	}

	taskID := evt.Entity.ID

	var completedAt *string
	tier := 0
	if p.NewStatus == "confirmed" {
		ts := kairosToISO(evt.TS)
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
			return err
		}
	} else {
		_, err := db.Exec(
			"UPDATE tasks SET status = ?, completed_at = ?, tier = ? WHERE id = ?",
			p.NewStatus, completedAt, tier, taskID,
		)
		if err != nil {
			return err
		}
	}

	if p.Outcome != "" {
		if _, err := db.Exec(
			"UPDATE tasks SET outcome = ? WHERE id = ?",
			p.Outcome, taskID,
		); err != nil {
			return err
		}
	}

	result.TasksUpdated++
	return nil
}

func replayTaskFieldsUpdated(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p TaskFieldsUpdatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return err
	}

	if len(p.Fields) == 0 {
		return nil
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
			continue
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
			args = append(args, kairosToISO(evt.TS))
		} else {
			setClauses = append(setClauses, "completed_at = NULL")
		}
	}

	if len(setClauses) == 0 {
		return nil
	}

	args = append(args, taskID)
	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", joinStrings(setClauses, ", "))

	if _, err := db.Exec(query, args...); err != nil {
		return err
	}
	result.TasksUpdated++
	return nil
}

func replayTaskMoved(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p TaskMovedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return err
	}

	if _, err := db.Exec("UPDATE tasks SET parent_id = ? WHERE id = ?",
		p.NewParentID, evt.Entity.ID); err != nil {
		return err
	}
	result.TasksUpdated++
	return nil
}

func replayTaskDeleted(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p TaskDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return err
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
			return err
		}
	} else {
		db.Exec("UPDATE tasks SET parent_id = NULL WHERE parent_id = ?", taskID)
		if _, err := db.Exec("DELETE FROM tasks WHERE id = ?", taskID); err != nil {
			return err
		}
	}
	result.TasksDeleted++
	return nil
}

func replayTaskBulkCleared(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p TaskBulkClearedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return err
	}

	projectID, err := ensureProject(db, evt.Project)
	if err != nil {
		return err
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
		return err
	}
	result.TasksDeleted++
	return nil
}

// ensureProject ensures the project exists in the temp DB, inserting a minimal row if needed.
func ensureProject(db *sql.DB, name string) (int64, error) {
	var id int64
	err := db.QueryRow("SELECT id FROM projects WHERE name = ?", name).Scan(&id)
	if err == nil {
		return id, nil
	}

	result, err := db.Exec(
		"INSERT INTO projects (name, path, status, created_at, updated_at) VALUES (?, '', 'active', '', '')",
		name,
	)
	if err != nil {
		return 0, fmt.Errorf("ensure project %q: %w", name, err)
	}
	id, _ = result.LastInsertId()
	return id, nil
}

// kairosToISO converts a kairos timestamp string to ISO 8601 for DB storage.
func kairosToISO(ts string) string {
	parsed, err := kairos.Parse(ts)
	if err != nil {
		return ""
	}
	return parsed.Physical().UTC().Format("2006-01-02T15:04:05")
}
