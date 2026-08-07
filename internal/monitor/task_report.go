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
	// Type is the focal task's type slug (todo/bugfix/research/epic/…), or ""
	// when the row has no type_id. Computed, not currently rendered: it gated
	// the epic-only Children list until E-1911 removed that list, and it stays
	// on the wire because the renderer's per-type decisions are the kind that
	// come back (Status and Landed are here on the same footing).
	Type string `json:"type"`
	// Landed is true when the task already has >=1 task_landings row. Almost
	// always false at report time (landing follows verification), so the prompt
	// mentions it only when true.
	Landed bool `json:"landed"`
	// Successors are downstream follow-up tasks: those this task `blocks`
	// (they wait on it) and those that `cleans_up` this task (post-ship
	// follow-ups filed against it). Each carries its current status so the agent
	// can tersely tell the user what was spawned and where it stands.
	Successors []TaskRef `json:"successors"`
	// Children is deliberately absent (E-1911). The report used to carry an
	// epic's children because the epic handoff asked the session to lead with
	// their state, and the handoff asked for it because the report computed it —
	// two surfaces each justifying the other while duplicating `session status`,
	// which is where a task's children are rendered correctly. Both are gone, so
	// the query goes too rather than lingering as an unread column.
}

// TaskRef is a lightweight reference to a related task: its id, current status,
// and the relation as seen from the focal task.
type TaskRef struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	// Relation is the successor direction: "blocks" (focal blocks this) or
	// "cleaned_up_by" (this cleans_up focal).
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

	// Focal status and type — the row must exist; a missing task is a caller
	// error, not an empty report. type_id is nullable, so LEFT JOIN + COALESCE
	// (an untyped task reports "", which is simply "not an epic").
	err := db.QueryRow(
		`SELECT t.status, COALESCE(tt.slug, '')
		   FROM tasks t
		   LEFT JOIN task_types tt ON tt.id = t.type_id
		  WHERE t.id = ?`, taskID,
	).Scan(&facts.Status, &facts.Type)
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
