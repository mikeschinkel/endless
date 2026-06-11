package events

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// autoAddUrgentPendingReason is the canonical reason text recorded on both
// the project_next_pending row and the pending.added event payload, so a
// reader of either can identify the auto-add origin without consulting the
// other. E-1437.
const autoAddUrgentPendingReason = "auto-added: phase=urgent"

// autoAddUrgentPending inserts a row into project_next_pending for the given
// task and emits a pending.added event in project_next_events. Called from
// execTaskCreated when the new task has phase=urgent, and from
// execTaskFieldsUpdated when an update sets phase to urgent.
//
// The INSERT uses ON CONFLICT(project_next_id, task_id) DO NOTHING — repeated
// triggers for the same task (urgent → next → urgent) leave only one pending
// row. When the insert is a no-op (rowsAffected == 0), the audit event is
// also suppressed: "we attempted to add but it was already there" isn't a
// state change, and the project_next_events log should record state changes,
// not trigger attempts (E-1437 Option A).
//
// project_next is created on demand if no row exists for the project yet —
// the pending FK requires a parent row.
//
// Runs inside the in-flight dispatcher transaction via the dbQuerier the
// caller passes. project_next_events.session_id is a NOT NULL FK to
// sessions(id); if evt.Actor.SessionID is empty or non-numeric (system
// actors, manual edits), the side effect is skipped entirely. In normal
// flow, task.created and task.fields_updated events from cli/hook actors
// always carry a session_id (emit_event in src/endless/event_bridge.py
// refuses to fire otherwise per E-1401).
func autoAddUrgentPending(db dbQuerier, evt *Event, taskID int64) error {
	sessionID, err := strconv.ParseInt(evt.Actor.SessionID, 10, 64)
	if err != nil {
		return nil
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return err
	}

	var projectNextID int64
	err = db.QueryRow(
		"SELECT id FROM project_next WHERE project_id = ?", projectID,
	).Scan(&projectNextID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, ierr := db.Exec(
			"INSERT INTO project_next (project_id) VALUES (?)", projectID,
		)
		if ierr != nil {
			return fmt.Errorf("auto-add pending: create project_next: %w", ierr)
		}
		projectNextID, _ = res.LastInsertId()
	case err != nil:
		return fmt.Errorf("auto-add pending: read project_next: %w", err)
	}

	res, err := db.Exec(
		`INSERT INTO project_next_pending (project_next_id, task_id, reason)
		 VALUES (?, ?, ?)
		 ON CONFLICT(project_next_id, task_id) DO NOTHING`,
		projectNextID, taskIDString(taskID), autoAddUrgentPendingReason,
	)
	if err != nil {
		return fmt.Errorf("auto-add pending: insert pending: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil
	}

	payload, err := json.Marshal(map[string]any{
		"task_id": taskIDString(taskID),
		"reason":  autoAddUrgentPendingReason,
		"trigger": string(evt.Kind),
	})
	if err != nil {
		return fmt.Errorf("auto-add pending: marshal event payload: %w", err)
	}

	if _, err := db.Exec(
		`INSERT INTO project_next_events
		   (project_next_id, session_id, kind, payload)
		 VALUES (?, ?, 'pending.added', ?)`,
		projectNextID, sessionID, string(payload),
	); err != nil {
		return fmt.Errorf("auto-add pending: insert event: %w", err)
	}
	return nil
}

// taskIDString formats an int64 task id as the bare "NNNN" form stored in
// project_next_pending.task_id (matches the project_next_tasks.task_id
// convention from E-1421). The display "E-NNNN" form is a presentation
// concern handled at render time.
func taskIDString(id int64) string {
	return strconv.FormatInt(id, 10)
}
