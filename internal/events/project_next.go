package events

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Item-count caps for the curated next list (E-1421). The soft cap warns but
// proceeds; the hard cap refuses. Enforced in ValidateProjectNextRevise, which
// runs BEFORE the write lock so a refusal writes nothing.
const (
	ProjectNextSoftCap = 10
	ProjectNextHardCap = 25
)

// ProjectNextResult is the executor's result for a project_next.revised event
// (E-1436). PriorRevision is the most recent revision that existed before this
// one (nil on the first revision) so the CLI can surface a cross-session
// collision notice. State echoes the applied list — revise is a full rewrite,
// so the input is the resulting state.
type ProjectNextResult struct {
	PriorRevision *ProjectNextRevisionRef
	State         ProjectNextState
}

// ProjectNextRevisionRef identifies a prior revision for the collision notice.
type ProjectNextRevisionRef struct {
	RevisedAt string `json:"revised_at"`
	SessionID int64  `json:"session_id"`
}

// ProjectNextState is the curated list as it stands after a revise.
type ProjectNextState struct {
	Project     string                   `json:"project"`
	LastRevised string                   `json:"last_revised"`
	SessionID   int64                    `json:"revised_by_session_id"`
	Lanes       []ProjectNextLanePayload `json:"lanes"`
}

// ValidateProjectNextRevise checks a revise payload's structure and the
// item-count caps BEFORE the write lock is taken (E-1436 gate). It returns a
// human-readable warning when the soft cap is exceeded (the command still
// proceeds) and an error when the payload is malformed or exceeds the hard cap
// (the command must refuse without writing anything). Non-integer priorities
// are rejected by the unmarshal itself.
func ValidateProjectNextRevise(payload []byte) (warning string, err error) {
	var p ProjectNextRevisedPayload
	if uerr := json.Unmarshal(payload, &p); uerr != nil {
		return "", fmt.Errorf("invalid revise payload: %w", uerr)
	}
	items := 0
	for i, lane := range p.Lanes {
		if strings.TrimSpace(lane.ID) == "" {
			return "", fmt.Errorf("lane %d: id is required", i)
		}
		if strings.TrimSpace(lane.Rationale) == "" {
			return "", fmt.Errorf("lane %q: rationale is required", lane.ID)
		}
		for j, item := range lane.Items {
			if strings.TrimSpace(item.TaskID) == "" {
				return "", fmt.Errorf("lane %q item %d: task_id is required", lane.ID, j)
			}
			if strings.TrimSpace(item.Reason) == "" {
				return "", fmt.Errorf("lane %q item %q: reason is required", lane.ID, item.TaskID)
			}
			items++
		}
	}
	if items > ProjectNextHardCap {
		return "", fmt.Errorf(
			"next list has %d items, exceeding the hard cap of %d; trim the list before revising",
			items, ProjectNextHardCap,
		)
	}
	if items > ProjectNextSoftCap {
		warning = fmt.Sprintf(
			"%d items exceeds the soft cap of %d; consider trimming the list",
			items, ProjectNextSoftCap,
		)
	}
	return warning, nil
}

// execProjectNextRevised applies a full rewrite of a project's curated next
// list. It runs under the BEGIN IMMEDIATE lock acquired by BeginImmediate, so
// the get-or-create read, the delete/reinsert, and the audit row are all
// serialized against any concurrent revise. Payload structure and caps are
// already validated (ValidateProjectNextRevise) before the lock was taken.
func execProjectNextRevised(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p ProjectNextRevisedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal project_next.revised payload: %w", err)
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	// Locate or create the per-project next-list header, and capture the most
	// recent prior revision (for the collision notice) before we overwrite.
	var (
		projectNextID int64
		prior         *ProjectNextRevisionRef
	)
	err = db.QueryRow("SELECT id FROM project_next WHERE project_id = ?", projectID).Scan(&projectNextID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		res, ierr := db.Exec("INSERT INTO project_next (project_id) VALUES (?)", projectID)
		if ierr != nil {
			return nil, fmt.Errorf("events: create project_next: %w", ierr)
		}
		projectNextID, _ = res.LastInsertId()
	case err != nil:
		return nil, fmt.Errorf("events: read project_next: %w", err)
	default:
		var (
			revisedAt string
			sessID    int64
		)
		rerr := db.QueryRow(
			`SELECT event_at, session_id FROM project_next_events
			 WHERE project_next_id = ? ORDER BY event_at DESC, id DESC LIMIT 1`,
			projectNextID,
		).Scan(&revisedAt, &sessID)
		switch {
		case rerr == nil:
			prior = &ProjectNextRevisionRef{RevisedAt: revisedAt, SessionID: sessID}
		case errors.Is(rerr, sql.ErrNoRows):
			// header exists but no revision recorded yet — treat as first
		default:
			return nil, fmt.Errorf("events: read prior revision: %w", rerr)
		}
	}

	// Full rewrite: drop existing lanes (items CASCADE), then reinsert.
	if _, err := db.Exec("DELETE FROM project_next_lanes WHERE project_next_id = ?", projectNextID); err != nil {
		return nil, fmt.Errorf("events: clear lanes: %w", err)
	}
	for _, lane := range p.Lanes {
		res, err := db.Exec(
			`INSERT INTO project_next_lanes (project_next_id, lane_id, priority, rationale)
			 VALUES (?, ?, ?, ?)`,
			projectNextID, lane.ID, lane.Priority, lane.Rationale,
		)
		if err != nil {
			return nil, fmt.Errorf("events: insert lane %q: %w", lane.ID, err)
		}
		laneID, _ := res.LastInsertId()
		for pos, item := range lane.Items {
			if _, err := db.Exec(
				`INSERT INTO project_next_tasks (project_next_lane_id, task_id, reason, position)
				 VALUES (?, ?, ?, ?)`,
				laneID, item.TaskID, item.Reason, pos,
			); err != nil {
				return nil, fmt.Errorf("events: insert item %q in lane %q: %w", item.TaskID, lane.ID, err)
			}
		}
	}

	// Audit row. session_id is a NOT NULL FK to sessions(id); emit_event
	// guarantees a session_id for cli actors before this point.
	sessionID := mustParseInt64(evt.Actor.SessionID)
	revisedAt := now()
	if _, err := db.Exec(
		`INSERT INTO project_next_events
		   (project_next_id, session_id, event_at, kind, payload)
		 VALUES (?, ?, ?, 'revise', ?)`,
		projectNextID, sessionID, revisedAt, string(evt.Payload),
	); err != nil {
		return nil, fmt.Errorf("events: insert revision: %w", err)
	}

	return &ExecuteResult{
		ProjectNext: &ProjectNextResult{
			PriorRevision: prior,
			State: ProjectNextState{
				Project:     evt.Project,
				LastRevised: revisedAt,
				SessionID:   sessionID,
				Lanes:       p.Lanes,
			},
		},
	}, nil
}
