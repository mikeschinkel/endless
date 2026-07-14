package monitor

import (
	"database/sql"
	"errors"
	"fmt"
)

// TaskReportFacts is the computed, non-agent-supplied half of a `task report`
// (E-1771). The reporting command feeds these facts into a steering prompt so
// the agent never types (and so can never dress up) a fact the tool can
// compute. It is deliberately NOT persisted here — capturing these as queryable
// rows is E-1777; this struct is a read-only snapshot for the prompt.
type TaskReportFacts struct {
	// TaskID is the focal task (the report's subject).
	TaskID int64 `json:"task_id"`
	// Status is the focal task's current status.
	Status string `json:"status"`
	// Landed is true when the task already has >=1 task_landings row. Almost
	// always false at report time (landing follows verification), so the prompt
	// mentions it only when true.
	Landed bool `json:"landed"`
	// Successors are downstream follow-up tasks: those this task `blocks`
	// (they wait on it) and those that `cleans_up` this task (post-ship
	// follow-ups filed against it). Each carries its current status so the agent
	// can tersely tell the user what was spawned and where it stands.
	Successors []TaskRef `json:"successors"`
	// Children are this task's direct child tasks (non-empty only when it is an
	// epic), with current status.
	Children []TaskRef `json:"children"`
}

// TaskRef is a lightweight reference to a related task: its id, current status,
// and (for successors) the relation as seen from the focal task.
type TaskRef struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	// Relation is set only for successors: "blocks" (focal blocks this) or
	// "cleaned_up_by" (this cleans_up focal). Empty for children.
	Relation string `json:"relation,omitempty"`
}

// BuildTaskReportFacts computes the report facts for a task against the global
// monitor DB. It is the exported entry point for the session-query handler;
// taskReportFacts is the db-taking core split out for tests (mirroring the
// reopen-context pattern).
func BuildTaskReportFacts(taskID int64) (TaskReportFacts, error) {
	db, err := DB()
	if err != nil {
		return TaskReportFacts{}, err
	}
	return taskReportFacts(db, taskID)
}

func taskReportFacts(db *sql.DB, taskID int64) (TaskReportFacts, error) {
	facts := TaskReportFacts{TaskID: taskID}

	// Focal status — the row must exist; a missing task is a caller error, not
	// an empty report.
	err := db.QueryRow("SELECT status FROM tasks WHERE id = ?", taskID).Scan(&facts.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return facts, fmt.Errorf("no such task E-%d", taskID)
	}
	if err != nil {
		return facts, fmt.Errorf("read status for E-%d: %w", taskID, err)
	}

	err = db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM task_landings WHERE task_id = ?)", taskID,
	).Scan(&facts.Landed)
	if err != nil {
		return facts, fmt.Errorf("read landed for E-%d: %w", taskID, err)
	}

	facts.Successors, err = taskSuccessors(db, taskID)
	if err != nil {
		return facts, err
	}

	facts.Children, err = taskChildren(db, taskID)
	if err != nil {
		return facts, err
	}

	return facts, nil
}

// taskSuccessors returns downstream follow-up tasks: the union of tasks this
// task blocks (focal is the `blocks` source) and tasks that clean up this task
// (focal is the `cleans_up` target). Ordered by id for stable output.
func taskSuccessors(db *sql.DB, taskID int64) ([]TaskRef, error) {
	rows, err := db.Query(
		`SELECT t.id AS id, t.status AS status, 'blocks' AS rel
		   FROM task_deps d JOIN tasks t ON t.id = d.target_id
		  WHERE d.source_id = ? AND d.source_type = 'task' AND d.target_type = 'task'
		    AND d.dep_type = 'blocks'
		 UNION
		 SELECT t.id AS id, t.status AS status, 'cleaned_up_by' AS rel
		   FROM task_deps d JOIN tasks t ON t.id = d.source_id
		  WHERE d.target_id = ? AND d.source_type = 'task' AND d.target_type = 'task'
		    AND d.dep_type = 'cleans_up'
		 ORDER BY id`,
		taskID, taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("query successors for E-%d: %w", taskID, err)
	}
	defer rows.Close()

	var refs []TaskRef
	for rows.Next() {
		var r TaskRef
		if err := rows.Scan(&r.ID, &r.Status, &r.Relation); err != nil {
			return nil, fmt.Errorf("scan successor for E-%d: %w", taskID, err)
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// taskChildren returns the focal task's direct children with current status,
// ordered by the tree's sort order.
func taskChildren(db *sql.DB, taskID int64) ([]TaskRef, error) {
	rows, err := db.Query(
		"SELECT id, status FROM tasks WHERE parent_id = ? ORDER BY sort_order, id",
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("query children for E-%d: %w", taskID, err)
	}
	defer rows.Close()

	var refs []TaskRef
	for rows.Next() {
		var r TaskRef
		if err := rows.Scan(&r.ID, &r.Status); err != nil {
			return nil, fmt.Errorf("scan child for E-%d: %w", taskID, err)
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}
