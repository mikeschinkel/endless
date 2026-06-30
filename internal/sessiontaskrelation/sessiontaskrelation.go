// Package sessiontaskrelation defines the Relation Go enum, the source of truth
// for session_tasks.relation_id (per ED-1506: const-in-code is the source of
// truth, the session_task_relations SQL table mirrors it). The package lives
// outside internal/events and internal/monitor so both can depend on it without
// a cycle.
//
// Relation classifies HOW a task entered a session's scope, set once at capture
// time by the task-mutation executors (E-1462):
//   - goal:      the session's claimed task (task.claimed)
//   - surfaced:  created during the session (task.created / task.imported)
//   - revisited: a pre-existing task the session touched but did not claim
//
// Adding a value = add an enum constant here + add a seed row in
// internal/schema/schema.sql + add a row in the per-ticket migration that
// introduces it. The VerifyIntegrity startup check fails closed on drift.
package sessiontaskrelation

import (
	"database/sql"
	"fmt"
)

// Relation is the closed enumeration of session-task scope-entry relations.
type Relation int

const (
	RelationGoal      Relation = 1
	RelationSurfaced  Relation = 2
	RelationRevisited Relation = 3
)

// String returns the lowercase machine slug (matches session_task_relations.slug).
func (r Relation) String() string {
	switch r {
	case RelationGoal:
		return "goal"
	case RelationSurfaced:
		return "surfaced"
	case RelationRevisited:
		return "revisited"
	default:
		return fmt.Sprintf("Relation(%d)", int(r))
	}
}

// Label returns the human display string (matches session_task_relations.label).
func (r Relation) Label() string {
	switch r {
	case RelationGoal:
		return "Goal"
	case RelationSurfaced:
		return "Surfaced"
	case RelationRevisited:
		return "Revisited"
	default:
		return ""
	}
}

// Parse converts a slug from CLI / external input to a Relation. Returns an
// error for unknown slugs.
func Parse(s string) (Relation, error) {
	switch s {
	case "goal":
		return RelationGoal, nil
	case "surfaced":
		return RelationSurfaced, nil
	case "revisited":
		return RelationRevisited, nil
	default:
		return 0, fmt.Errorf(
			"sessiontaskrelation: invalid relation %q (valid: goal, surfaced, revisited)", s,
		)
	}
}

// Validate returns an error if s is not a recognized slug.
func Validate(s string) error {
	_, err := Parse(s)
	return err
}

// All returns the canonical set in id order. Used by VerifyIntegrity and by
// callers that need to enumerate the enum.
func All() []Relation {
	return []Relation{RelationGoal, RelationSurfaced, RelationRevisited}
}

// VerifyIntegrity asserts that the session_task_relations SQL table matches the
// Go enum. Runs once at startup (from monitor.DB() after schema.SQL applies).
// Returns an error on any drift: an enum constant with no matching row, a slug
// or label mismatch, or a session_task_relations row whose id does not match any
// constant. Callers are expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	type row struct {
		id    int
		slug  string
		label string
	}
	rows, err := db.Query("SELECT id, slug, label FROM session_task_relations")
	if err != nil {
		return fmt.Errorf("sessiontaskrelation: query session_task_relations: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.label); err != nil {
			return fmt.Errorf("sessiontaskrelation: scan session_task_relations row: %w", err)
		}
		byID[r.id] = r
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sessiontaskrelation: iterate session_task_relations: %w", err)
	}

	for _, rel := range All() {
		r, ok := byID[int(rel)]
		if !ok {
			return fmt.Errorf(
				"sessiontaskrelation: enum constant %s (id=%d) missing from session_task_relations table",
				rel.String(), int(rel))
		}
		if r.slug != rel.String() {
			return fmt.Errorf("sessiontaskrelation: id=%d slug mismatch: enum=%q, table=%q",
				int(rel), rel.String(), r.slug)
		}
		if r.label != rel.Label() {
			return fmt.Errorf("sessiontaskrelation: id=%d label mismatch: enum=%q, table=%q",
				int(rel), rel.Label(), r.label)
		}
		delete(byID, int(rel))
	}

	for id, r := range byID {
		return fmt.Errorf(
			"sessiontaskrelation: session_task_relations row id=%d slug=%q has no matching enum constant",
			id, r.slug)
	}

	return nil
}
