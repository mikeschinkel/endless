package events

import (
	"database/sql"
	"fmt"
)

// Mismatch describes a difference between projected and current task state.
type Mismatch struct {
	TaskID  int64
	Title   string
	Field   string
	Current string
	Projected string
}

// MissingTask describes a task that exists in one DB but not the other.
type MissingTask struct {
	TaskID int64
	Title  string
	In     string // "current" or "projected"
}

// ValidationResult holds the outcome of comparing projected vs current DB.
type ValidationResult struct {
	TasksCompared int
	Mismatches    []Mismatch
	MissingTasks  []MissingTask
}

// ValidateTasks compares the tasks table between the projected temp DB and the current DB.
// Only compares tasks that have events (i.e., tasks created/modified through the event system).
func ValidateTasks(currentDB *sql.DB, projectedDBPath string) (*ValidationResult, error) {
	projDB, err := sql.Open("sqlite", projectedDBPath)
	if err != nil {
		return nil, fmt.Errorf("validator: open projected db: %w", err)
	}
	defer projDB.Close()

	result := &ValidationResult{}

	// Get all task IDs from projected DB
	projTasks, err := loadTasks(projDB)
	if err != nil {
		return nil, fmt.Errorf("validator: load projected tasks: %w", err)
	}

	// Get matching tasks from current DB
	curTasks, err := loadTasks(currentDB)
	if err != nil {
		return nil, fmt.Errorf("validator: load current tasks: %w", err)
	}

	// Find tasks in projected but not current
	for id, pt := range projTasks {
		ct, exists := curTasks[id]
		if !exists {
			result.MissingTasks = append(result.MissingTasks, MissingTask{
				TaskID: id, Title: pt.title, In: "projected",
			})
			continue
		}

		result.TasksCompared++
		compareTasks(id, pt, ct, result)
	}

	// Find tasks in current but not projected (only if they could have events)
	// Skip this for now -- tasks created before event system won't have events
	// and would all show as "missing from projected"

	return result, nil
}

type taskRow struct {
	title       string
	description string
	phase       string
	status      string
	taskType    string
	parentID    *int64
	tier        *int
}

func loadTasks(db *sql.DB) (map[int64]taskRow, error) {
	rows, err := db.Query(
		`SELECT id, COALESCE(title,''), COALESCE(description,''),
		 phase, status, COALESCE(type,'task'), parent_id, tier
		 FROM tasks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := make(map[int64]taskRow)
	for rows.Next() {
		var id int64
		var t taskRow
		if err := rows.Scan(&id, &t.title, &t.description, &t.phase, &t.status, &t.taskType, &t.parentID, &t.tier); err != nil {
			return nil, err
		}
		tasks[id] = t
	}
	return tasks, nil
}

func fmtOptInt64(v *int64) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *v)
}

func fmtOptInt(v *int) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *v)
}

func compareTasks(id int64, proj, cur taskRow, result *ValidationResult) {
	check := func(field, pVal, cVal string) {
		if pVal != cVal {
			result.Mismatches = append(result.Mismatches, Mismatch{
				TaskID: id, Title: cur.title, Field: field,
				Projected: pVal, Current: cVal,
			})
		}
	}

	check("title", proj.title, cur.title)
	check("description", proj.description, cur.description)
	check("phase", proj.phase, cur.phase)
	check("status", proj.status, cur.status)
	check("type", proj.taskType, cur.taskType)

	check("parent_id", fmtOptInt64(proj.parentID), fmtOptInt64(cur.parentID))
	check("tier", fmtOptInt(proj.tier), fmtOptInt(cur.tier))
}
