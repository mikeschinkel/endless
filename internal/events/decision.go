package events

import (
	"encoding/json"
	"fmt"
)

// Executor functions for decision and decision_relation events (E-1378).
//
// Decisions live in their own table, separate from tasks, with a 3-state
// lifecycle: proposed (initial) -> accepted | rejected (both terminal).
// Decision-sourced relations live in decision_relations (target_kind can be
// 'task' or 'decision'); task-sourced relations stay in task_deps until
// E-1389 renames it.

var allowedDecisionFields = map[string]string{
	"title":            "title",
	"description":      "description",
	"text":             "text",
	"notes":            "notes",
	"origin_task_id":   "origin_task_id",
	"rejection_reason": "rejection_reason",
}

// validDecisionStatuses gates the status column in application code; there
// is no CHECK constraint in schema.sql (schema.sql line 11-13 forbids them).
var validDecisionStatuses = map[string]bool{
	"proposed": true,
	"accepted": true,
	"rejected": true,
}

// validRelationTargetKinds: decision_relations.target_kind is 'task' or
// 'decision'; relation_type validation is per-pair and lives in the Python
// CLI (matches the verb dispatchers).
var validRelationTargetKinds = map[string]bool{
	"task":     true,
	"decision": true,
}

func execDecisionCreated(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.created payload: %w", err)
	}

	status := p.Status
	if status == "" {
		status = "proposed"
	}
	if !validDecisionStatuses[status] {
		return nil, fmt.Errorf("events: invalid decision status %q", status)
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	decisionID := mustParseInt64(evt.Entity.ID)
	ts := now()

	// origin_task_id and origin_session_id are stored as NULL when zero so
	// the FK ON DELETE SET NULL machinery has a value to set null *to*.
	var originTaskID, originSessionID any
	if p.OriginTaskID != 0 {
		originTaskID = p.OriginTaskID
	}
	if p.OriginSessionID != 0 {
		originSessionID = p.OriginSessionID
	}

	_, err = db.Exec(
		`INSERT INTO decisions
		   (id, project_id, title, description, text, status,
		    origin_task_id, origin_session_id, notes,
		    created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		decisionID, projectID, p.Title, p.Description, p.Text, status,
		originTaskID, originSessionID, p.Notes, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert decision: %w", err)
	}

	return &ExecuteResult{DecisionID: decisionID}, nil
}

func execDecisionFieldsUpdated(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionFieldsUpdatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.fields_updated payload: %w", err)
	}
	if len(p.Fields) == 0 {
		return &ExecuteResult{}, nil
	}

	decisionID := evt.Entity.ID

	var setClauses []string
	var args []any

	for field, value := range p.Fields {
		col, ok := allowedDecisionFields[field]
		if !ok {
			return nil, fmt.Errorf("events: unknown field %q in decision.fields_updated", field)
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, value)
	}

	args = append(args, decisionID)
	query := fmt.Sprintf("UPDATE decisions SET %s WHERE id = ?",
		joinStrings(setClauses, ", "))

	if _, err := db.Exec(query, args...); err != nil {
		return nil, fmt.Errorf("events: update decision fields: %w", err)
	}

	return &ExecuteResult{}, nil
}

func execDecisionAccepted(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	// Payload is empty (DecisionAcceptedPayload{}); we still parse it to
	// validate the JSON structure.
	var p DecisionAcceptedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.accepted payload: %w", err)
	}

	result, err := db.Exec(
		`UPDATE decisions SET status = 'accepted' WHERE id = ? AND status = 'proposed'`,
		evt.Entity.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: accept decision: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		// Either the decision is gone or it isn't in `proposed`. Surface the
		// real shape so the CLI can render a helpful message.
		var status string
		row := db.QueryRow("SELECT status FROM decisions WHERE id = ?", evt.Entity.ID)
		if err := row.Scan(&status); err != nil {
			return nil, fmt.Errorf("events: accept decision %s: not found", evt.Entity.ID)
		}
		return nil, fmt.Errorf("events: accept decision %s: status is %q, expected proposed",
			evt.Entity.ID, status)
	}
	return &ExecuteResult{}, nil
}

func execDecisionRejected(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionRejectedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.rejected payload: %w", err)
	}
	if p.Reason == "" {
		return nil, fmt.Errorf("events: decision.rejected requires non-empty reason")
	}

	result, err := db.Exec(
		`UPDATE decisions
		    SET status = 'rejected', rejection_reason = ?
		  WHERE id = ? AND status = 'proposed'`,
		p.Reason, evt.Entity.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: reject decision: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		var status string
		row := db.QueryRow("SELECT status FROM decisions WHERE id = ?", evt.Entity.ID)
		if err := row.Scan(&status); err != nil {
			return nil, fmt.Errorf("events: reject decision %s: not found", evt.Entity.ID)
		}
		return nil, fmt.Errorf("events: reject decision %s: status is %q, expected proposed",
			evt.Entity.ID, status)
	}
	return &ExecuteResult{}, nil
}

func execDecisionDeleted(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	// Payload is informational (title for log-trail); the DELETE keys off
	// entity ID. decision_relations rows cascade via the FK.
	var p DecisionDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.deleted payload: %w", err)
	}
	_ = p // title is for the ledger trail only

	if _, err := db.Exec(
		"DELETE FROM decisions WHERE id = ?",
		evt.Entity.ID,
	); err != nil {
		return nil, fmt.Errorf("events: delete decision: %w", err)
	}
	return &ExecuteResult{}, nil
}

func execDecisionRelationCreated(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionRelationCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision_relation.created payload: %w", err)
	}
	if !validRelationTargetKinds[p.TargetKind] {
		return nil, fmt.Errorf("events: invalid decision_relation target_kind %q", p.TargetKind)
	}
	if p.RelationType == "" {
		return nil, fmt.Errorf("events: decision_relation.created requires relation_type")
	}

	_, err := db.Exec(
		`INSERT INTO decision_relations
		   (source_decision_id, target_kind, target_id, relation_type)
		 VALUES (?, ?, ?, ?)`,
		p.SourceDecisionID, p.TargetKind, p.TargetID, p.RelationType,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert decision_relation: %w", err)
	}
	return &ExecuteResult{}, nil
}

func execDecisionRelationDeleted(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionRelationDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision_relation.deleted payload: %w", err)
	}

	result, err := db.Exec(
		`DELETE FROM decision_relations
		  WHERE source_decision_id = ? AND target_kind = ? AND target_id = ? AND relation_type = ?`,
		p.SourceDecisionID, p.TargetKind, p.TargetID, p.RelationType,
	)
	if err != nil {
		return nil, fmt.Errorf("events: delete decision_relation: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("events: delete decision_relation: no matching row")
	}
	return &ExecuteResult{}, nil
}
