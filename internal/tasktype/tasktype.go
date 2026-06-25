// Package tasktype defines the TaskType Go enum, the source of truth for
// tasks.type_id (per ED-1506: const-in-code is the source of truth, the
// task_types SQL table mirrors it). The package lives outside internal/events
// and internal/monitor so both can depend on it without a cycle.
//
// Adding a value = add an enum constant here + add a seed row in
// internal/schema/schema.sql + add a row in the per-ticket migration that
// introduces it. The VerifyIntegrity startup check fails closed on drift.
package tasktype

import (
	"database/sql"
	"fmt"
)

// TaskType is the closed enumeration of task type values.
type TaskType int

const (
	TaskTypeTask       TaskType = 1
	TaskTypeBug        TaskType = 2
	TaskTypeResearch   TaskType = 3
	TaskTypeEpic       TaskType = 4
	TaskTypeBrainstorm TaskType = 5
)

// String returns the lowercase machine slug (matches task_types.slug).
func (t TaskType) String() string {
	switch t {
	case TaskTypeTask:
		return "task"
	case TaskTypeBug:
		return "bug"
	case TaskTypeResearch:
		return "research"
	case TaskTypeEpic:
		return "epic"
	case TaskTypeBrainstorm:
		return "brainstorm"
	default:
		return fmt.Sprintf("TaskType(%d)", int(t))
	}
}

// Label returns the human display string (matches task_types.label).
func (t TaskType) Label() string {
	switch t {
	case TaskTypeTask:
		return "Task"
	case TaskTypeBug:
		return "Bug"
	case TaskTypeResearch:
		return "Research"
	case TaskTypeEpic:
		return "Epic"
	case TaskTypeBrainstorm:
		return "Brainstorm"
	default:
		return ""
	}
}

// Parse converts a slug from CLI / external input to a TaskType. Returns an
// error for unknown slugs.
func Parse(s string) (TaskType, error) {
	switch s {
	case "task":
		return TaskTypeTask, nil
	case "bug":
		return TaskTypeBug, nil
	case "research":
		return TaskTypeResearch, nil
	case "epic":
		return TaskTypeEpic, nil
	case "brainstorm":
		return TaskTypeBrainstorm, nil
	default:
		return 0, fmt.Errorf("tasktype: invalid task type %q (valid: task, bug, research, epic, brainstorm)", s)
	}
}

// Validate returns an error if s is not a recognized slug. Used by the events
// write path before the DB is touched.
func Validate(s string) error {
	_, err := Parse(s)
	return err
}

// All returns the canonical set in id order. Used by VerifyIntegrity and by
// callers that need to enumerate the enum (e.g., to render a picker).
func All() []TaskType {
	return []TaskType{TaskTypeTask, TaskTypeBug, TaskTypeResearch, TaskTypeEpic, TaskTypeBrainstorm}
}

// VerifyIntegrity asserts that the task_types SQL table matches the Go enum.
// Runs once at startup (from monitor.DB() after schema.SQL applies). Returns
// an error on any drift: an enum constant with no matching row, a slug or
// label mismatch, or a task_types row whose id does not match any constant.
// Callers are expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	type row struct {
		id    int
		slug  string
		label string
	}
	rows, err := db.Query("SELECT id, slug, label FROM task_types")
	if err != nil {
		return fmt.Errorf("tasktype: query task_types: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.label); err != nil {
			return fmt.Errorf("tasktype: scan task_types row: %w", err)
		}
		byID[r.id] = r
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("tasktype: iterate task_types: %w", err)
	}

	for _, tt := range All() {
		r, ok := byID[int(tt)]
		if !ok {
			return fmt.Errorf("tasktype: enum constant %s (id=%d) missing from task_types table",
				tt.String(), int(tt))
		}
		if r.slug != tt.String() {
			return fmt.Errorf("tasktype: id=%d slug mismatch: enum=%q, table=%q",
				int(tt), tt.String(), r.slug)
		}
		if r.label != tt.Label() {
			return fmt.Errorf("tasktype: id=%d label mismatch: enum=%q, table=%q",
				int(tt), tt.Label(), r.label)
		}
		delete(byID, int(tt))
	}

	for id, r := range byID {
		return fmt.Errorf("tasktype: task_types row id=%d slug=%q has no matching enum constant",
			id, r.slug)
	}

	return nil
}
