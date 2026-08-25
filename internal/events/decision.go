package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Executor functions for decision and decision_relation events (E-1378).
//
// Decisions live in their own table, separate from tasks, with a 5-state
// lifecycle: proposed (initial) -> accepted | rejected, and accepted ->
// superseded | obsolete (E-1920, both terminal).
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
//
// `superseded` and `obsolete` (E-1920) are listed because decision.created
// validates against this map, and a projector replaying a legacy import may
// legitimately land a decision straight into an end state. The FORWARD
// transitions into them are narrower than this map — each executor guards on
// `status = 'accepted'` — so listing them here does not make them reachable
// from anywhere the CLI would refuse.
var validDecisionStatuses = map[string]bool{
	"proposed":   true,
	"accepted":   true,
	"rejected":   true,
	"superseded": true,
	"obsolete":   true,
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

// execDecisionUnaccepted reverses an accept: accepted -> proposed (E-1864).
//
// The guard is `status = 'accepted'`, not "any terminal status", so pointing
// unaccept at a *rejected* decision fails loudly instead of quietly performing
// the other reversal.
func execDecisionUnaccepted(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionUnacceptedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.unaccepted payload: %w", err)
	}

	result, err := db.Exec(
		`UPDATE decisions SET status = 'proposed' WHERE id = ? AND status = 'accepted'`,
		evt.Entity.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: unaccept decision: %w", err)
	}
	if err := requireDecisionRowAffected(db, result, evt.Entity.ID, "unaccept", "accepted"); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

// execDecisionUnrejected reverses a reject: rejected -> proposed (E-1864).
//
// rejection_reason is cleared in the same statement: it describes why the
// decision was rejected, and a decision back in 'proposed' has not been
// rejected. Leaving it would render a stale "Reason:" line on a proposed
// decision. The reason itself survives in the decision.rejected ledger entry.
func execDecisionUnrejected(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionUnrejectedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.unrejected payload: %w", err)
	}

	result, err := db.Exec(
		`UPDATE decisions
		    SET status = 'proposed', rejection_reason = NULL
		  WHERE id = ? AND status = 'rejected'`,
		evt.Entity.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: unreject decision: %w", err)
	}
	if err := requireDecisionRowAffected(db, result, evt.Entity.ID, "unreject", "rejected"); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

// execDecisionSuperseded records that a newer decision took over: accepted ->
// superseded (E-1920).
//
// The guard is `status = 'accepted'` because only an accepted decision
// governs, so only an accepted decision can stop governing. Superseding a
// `proposed` decision is a category error — nothing was in force to hand over
// — and superseding an already-`obsolete` one would overwrite the truer fact.
//
// The replacement's identity is NOT written here. It lives in the
// `supersedes` row in decision_relations, emitted as its own
// decision_relation.created event by the caller, which keeps one fact in one
// place and lets the relation be read by the same machinery as every other
// decision link. The payload's id is ledger provenance only.
func execDecisionSuperseded(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionSupersededPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.superseded payload: %w", err)
	}
	if p.BySupersedingID == 0 {
		return nil, fmt.Errorf("events: decision.superseded requires by_superseding_id")
	}
	if fmt.Sprint(p.BySupersedingID) == evt.Entity.ID {
		return nil, fmt.Errorf("events: decision %s cannot supersede itself", evt.Entity.ID)
	}

	result, err := db.Exec(
		`UPDATE decisions SET status = 'superseded' WHERE id = ? AND status = 'accepted'`,
		evt.Entity.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: supersede decision: %w", err)
	}
	if err = requireDecisionRowAffected(db, result, evt.Entity.ID, "supersede", "accepted"); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

// execDecisionObsoleted records that a decision stopped applying with no
// replacement: accepted -> obsolete (E-1920).
//
// Same `accepted` guard as supersede, for the same reason. The reason is
// stored on the row rather than left to the ledger because it is the only
// thing distinguishing "retired deliberately" from "quietly stopped being
// mentioned", and a reader hitting the decision needs it inline.
func execDecisionObsoleted(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionObsoletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.obsoleted payload: %w", err)
	}
	if p.Reason == "" {
		return nil, fmt.Errorf("events: decision.obsoleted requires non-empty reason")
	}

	result, err := db.Exec(
		`UPDATE decisions
		    SET status = 'obsolete', obsolete_reason = ?
		  WHERE id = ? AND status = 'accepted'`,
		p.Reason, evt.Entity.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: obsolete decision: %w", err)
	}
	if err = requireDecisionRowAffected(db, result, evt.Entity.ID, "obsolete", "accepted"); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

// execDecisionReinstated reverses either end state: superseded | obsolete ->
// accepted (E-1920).
//
// ONE reversal for two forward transitions, unlike the E-1864 pair. That is
// not a departure from their precedent but a consequence of it: those two
// guard narrowly so undoing the wrong one errors, and the risk they guard
// against is the destination being wrong. Here both end states are reachable
// only FROM `accepted`, so both reversals have the same destination and there
// is no wrong one to land on.
//
// obsolete_reason is cleared in the same statement, exactly as unreject clears
// rejection_reason: a decision back in `accepted` has not been obsoleted, and
// leaving the text would render a stale "Obsolete:" line on a live decision.
// The reason survives in the decision.obsoleted ledger entry. The `supersedes`
// relation is retired by the caller, which emits decision_relation.deleted for
// it — the mirror of the split on the way in.
func execDecisionReinstated(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p DecisionReinstatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal decision.reinstated payload: %w", err)
	}

	result, err := db.Exec(
		`UPDATE decisions
		    SET status = 'accepted', obsolete_reason = NULL
		  WHERE id = ? AND status IN ('superseded', 'obsolete')`,
		evt.Entity.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: reinstate decision: %w", err)
	}
	if err = requireDecisionRowAffected(
		db, result, evt.Entity.ID, "reinstate", "superseded or obsolete",
	); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

// requireDecisionRowAffected turns a zero-row status transition into an error
// that names the status actually found, so the CLI can render something more
// useful than "nothing happened". Shared by the E-1864 reversals; accept and
// reject predate it and keep their inlined equivalents.
func requireDecisionRowAffected(
	db dbQuerier, result sql.Result, id string, verb string, want string,
) error {
	n, _ := result.RowsAffected()
	if n > 0 {
		return nil
	}
	var status string
	row := db.QueryRow("SELECT status FROM decisions WHERE id = ?", id)
	if err := row.Scan(&status); err != nil {
		return fmt.Errorf("events: %s decision %s: not found", verb, id)
	}
	return fmt.Errorf("events: %s decision %s: status is %q, expected %s",
		verb, id, status, want)
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

// =====================================================================
// Projector replay functions (rebuild-db path).
//
// The projector reads JSONL events and produces a fresh DB projection. For
// decision events it inserts/updates the decisions and decision_relations
// tables directly. Lossless replay of legacy task.created events with
// type='decision' is handled in projector.go (replayTaskCreated routes them
// here via mapLegacyDecisionStatus + a direct INSERT into decisions).
// =====================================================================

// mapLegacyDecisionStatus reflects the pre-E-1378 status vocabulary onto the
// new 3-state lifecycle. Mirrors the SQL mapping in
// internal/schema/changes/e-1378-extract-decisions.sql so replay and
// change-file produce the same projection.
//
// E-1891 deliberately did NOT route these through internal/taskstatus. This
// reads a FROZEN historical vocabulary — whatever those rows said when the
// change file ran — and must keep answering the same way forever. Pointing it
// at the live registry would make a future status silently change how old
// decisions project.
func mapLegacyDecisionStatus(legacy string) string {
	switch legacy {
	case "confirmed", "completed", "assumed":
		return "accepted"
	case "unplanned", "ready":
		return "proposed"
	default:
		return "accepted"
	}
}

func replayDecisionCreated(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p DecisionCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("unmarshal decision.created: %w", err)
	}
	projectID, err := ensureProject(db, evt.Project)
	if err != nil {
		return err
	}

	status := p.Status
	if status == "" {
		status = "proposed"
	}
	if !validDecisionStatuses[status] {
		return fmt.Errorf("invalid decision status %q", status)
	}

	decisionID := mustParseInt64(evt.Entity.ID)
	ts := kairosToISO(evt.TS)

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
		return fmt.Errorf("insert decision %d: %w", decisionID, err)
	}
	return nil
}

func replayDecisionFieldsUpdated(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p DecisionFieldsUpdatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("unmarshal decision.fields_updated: %w", err)
	}
	if len(p.Fields) == 0 {
		return nil
	}

	var setClauses []string
	var args []any
	for field, value := range p.Fields {
		col, ok := allowedDecisionFields[field]
		if !ok {
			return fmt.Errorf("unknown field %q in decision.fields_updated", field)
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, value)
	}
	args = append(args, evt.Entity.ID)
	query := fmt.Sprintf("UPDATE decisions SET %s WHERE id = ?",
		joinStrings(setClauses, ", "))
	if _, err := db.Exec(query, args...); err != nil {
		return fmt.Errorf("update decision %s fields: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionAccepted(db *sql.DB, evt *Event, result *ProjectResult) error {
	_, err := db.Exec(
		`UPDATE decisions SET status = 'accepted' WHERE id = ? AND status = 'proposed'`,
		evt.Entity.ID,
	)
	if err != nil {
		return fmt.Errorf("accept decision %s: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionRejected(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p DecisionRejectedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("unmarshal decision.rejected: %w", err)
	}
	_, err := db.Exec(
		`UPDATE decisions
		    SET status = 'rejected', rejection_reason = ?
		  WHERE id = ? AND status = 'proposed'`,
		p.Reason, evt.Entity.ID,
	)
	if err != nil {
		return fmt.Errorf("reject decision %s: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionUnaccepted(db *sql.DB, evt *Event, result *ProjectResult) error {
	_, err := db.Exec(
		`UPDATE decisions SET status = 'proposed' WHERE id = ? AND status = 'accepted'`,
		evt.Entity.ID,
	)
	if err != nil {
		return fmt.Errorf("unaccept decision %s: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionUnrejected(db *sql.DB, evt *Event, result *ProjectResult) error {
	_, err := db.Exec(
		`UPDATE decisions
		    SET status = 'proposed', rejection_reason = NULL
		  WHERE id = ? AND status = 'rejected'`,
		evt.Entity.ID,
	)
	if err != nil {
		return fmt.Errorf("unreject decision %s: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionSuperseded(db *sql.DB, evt *Event, result *ProjectResult) error {
	_, err := db.Exec(
		`UPDATE decisions SET status = 'superseded' WHERE id = ? AND status = 'accepted'`,
		evt.Entity.ID,
	)
	if err != nil {
		return fmt.Errorf("supersede decision %s: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionObsoleted(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p DecisionObsoletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("unmarshal decision.obsoleted: %w", err)
	}
	_, err := db.Exec(
		`UPDATE decisions
		    SET status = 'obsolete', obsolete_reason = ?
		  WHERE id = ? AND status = 'accepted'`,
		p.Reason, evt.Entity.ID,
	)
	if err != nil {
		return fmt.Errorf("obsolete decision %s: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionReinstated(db *sql.DB, evt *Event, result *ProjectResult) error {
	_, err := db.Exec(
		`UPDATE decisions
		    SET status = 'accepted', obsolete_reason = NULL
		  WHERE id = ? AND status IN ('superseded', 'obsolete')`,
		evt.Entity.ID,
	)
	if err != nil {
		return fmt.Errorf("reinstate decision %s: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionDeleted(db *sql.DB, evt *Event, result *ProjectResult) error {
	if _, err := db.Exec(
		"DELETE FROM decisions WHERE id = ?",
		evt.Entity.ID,
	); err != nil {
		return fmt.Errorf("delete decision %s: %w", evt.Entity.ID, err)
	}
	return nil
}

func replayDecisionRelationCreated(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p DecisionRelationCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("unmarshal decision_relation.created: %w", err)
	}
	if _, err := db.Exec(
		`INSERT INTO decision_relations
		   (source_decision_id, target_kind, target_id, relation_type)
		 VALUES (?, ?, ?, ?)`,
		p.SourceDecisionID, p.TargetKind, p.TargetID, p.RelationType,
	); err != nil {
		return fmt.Errorf("insert decision_relation: %w", err)
	}
	return nil
}

func replayDecisionRelationDeleted(db *sql.DB, evt *Event, result *ProjectResult) error {
	var p DecisionRelationDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("unmarshal decision_relation.deleted: %w", err)
	}
	if _, err := db.Exec(
		`DELETE FROM decision_relations
		  WHERE source_decision_id = ? AND target_kind = ? AND target_id = ? AND relation_type = ?`,
		p.SourceDecisionID, p.TargetKind, p.TargetID, p.RelationType,
	); err != nil {
		return fmt.Errorf("delete decision_relation: %w", err)
	}
	return nil
}

// replayLegacyDecisionCreated handles pre-E-1378 task.created events where
// the payload's type was 'decision'. It inserts the row into the decisions
// table with the legacy status mapped through mapLegacyDecisionStatus so
// the replay projection matches the post-change-file shape.
//
// Called from projector.go's replayTaskCreated when payload.Type == "decision".
func replayLegacyDecisionCreated(db *sql.DB, evt *Event, p *TaskCreatedPayload) error {
	projectID, err := ensureProject(db, evt.Project)
	if err != nil {
		return err
	}
	decisionID := mustParseInt64(evt.Entity.ID)
	ts := kairosToISO(evt.TS)
	status := mapLegacyDecisionStatus(p.Status)
	_, err = db.Exec(
		`INSERT INTO decisions
		   (id, project_id, title, description, text, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		decisionID, projectID, p.Title, p.Description, p.Text, status, ts, ts,
	)
	if err != nil {
		return fmt.Errorf("insert legacy decision %d: %w", decisionID, err)
	}
	return nil
}
