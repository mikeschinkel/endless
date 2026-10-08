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
		return "todo"
	case TaskTypeBug:
		return "bugfix"
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
		return "Todo"
	case TaskTypeBug:
		return "Bugfix"
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

// AutoSpawnable reports whether the auto-spawn job (E-1814) may start a task
// of this type without anyone asking. Only implementation work qualifies: its
// deliverable is behavior a person verifies afterwards. Research, brainstorm
// and epic work deliver findings or coordination that a person steers while it
// happens, so a session nobody asked for has nothing to hand back.
//
// Mirrored into task_types.auto_spawnable, which is what the selector filters
// on; VerifyIntegrity fails closed if the two disagree.
func (t TaskType) AutoSpawnable() bool {
	switch t {
	case TaskTypeTask, TaskTypeBug:
		return true
	default:
		return false
	}
}

// Lands reports whether `endless worktree land` accepts a task of this type at
// all (E-2262). research and brainstorm deliver their outcome text, never files,
// so there is nothing on a branch for a land to merge.
//
// Mirrored into task_types.lands; VerifyIntegrity fails closed on drift.
func (t TaskType) Lands() bool {
	switch t {
	case TaskTypeResearch, TaskTypeBrainstorm:
		return false
	default:
		return true
	}
}

// RequiresVerifySuite reports whether a land needs a passing user verify — the
// task at `unlanded` — even when the task has no suite (E-2262). True for the
// implementation types, whose deliverable is behavior a suite can prove. A type
// without it is still gated when the task HAS a suite and the type's lifecycle
// includes `unlanded`; it just may land without one.
//
// Mirrored into task_types.requires_verify_suite.
func (t TaskType) RequiresVerifySuite() bool {
	switch t {
	case TaskTypeTask, TaskTypeBug:
		return true
	default:
		return false
	}
}

// SettlesOnLand reports whether a successful land sets a task of this type to
// `assumed` (E-2262): the work is merged and verified, and further confirmation
// comes from use.
//
// Mirrored into task_types.settles_on_land.
func (t TaskType) SettlesOnLand() bool {
	switch t {
	case TaskTypeTask, TaskTypeBug:
		return true
	default:
		return false
	}
}

// Parse converts a slug from CLI / external input to a TaskType. Returns an
// error for unknown slugs.
//
// The legacy slugs 'task' and 'bug' are accepted as aliases for their
// current names 'todo' and 'bugfix'. type_id is a stable integer
// (TaskTypeTask=1, TaskTypeBug=2) and historical `task.created` /
// `task.fields_updated` events carry the OLD slug string; every replay path
// resolves the type through this one chokepoint (executor + projectorTypeID),
// so accepting the aliases lets those events replay to the correct type_id
// with no DB rewrite. String() still emits only the current 'todo'/'bugfix'.
func Parse(s string) (TaskType, error) {
	switch s {
	case "todo", "task":
		return TaskTypeTask, nil
	case "bugfix", "bug":
		return TaskTypeBug, nil
	case "research":
		return TaskTypeResearch, nil
	case "epic":
		return TaskTypeEpic, nil
	case "brainstorm":
		return TaskTypeBrainstorm, nil
	default:
		return 0, fmt.Errorf("tasktype: invalid task type %q (valid: todo, bugfix, research, epic, brainstorm)", s)
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
// an error on any drift: an enum constant with no matching row, a slug, label,
// auto_spawnable or land-property mismatch, or a task_types row whose id does
// not match any constant.
// Callers are expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	type row struct {
		id                  int
		slug                string
		label               string
		autoSpawnable       bool
		lands               bool
		requiresVerifySuite bool
		settlesOnLand       bool
	}
	rows, err := db.Query(`SELECT id, slug, label, auto_spawnable,
		lands, requires_verify_suite, settles_on_land FROM task_types`)
	if err != nil {
		return fmt.Errorf("tasktype: query task_types: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.label, &r.autoSpawnable,
			&r.lands, &r.requiresVerifySuite, &r.settlesOnLand); err != nil {
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
		if r.autoSpawnable != tt.AutoSpawnable() {
			return fmt.Errorf("tasktype: id=%d auto_spawnable mismatch: enum=%t, table=%t",
				int(tt), tt.AutoSpawnable(), r.autoSpawnable)
		}
		for _, f := range []struct {
			column      string
			enum, table bool
		}{
			{"lands", tt.Lands(), r.lands},
			{"requires_verify_suite", tt.RequiresVerifySuite(), r.requiresVerifySuite},
			{"settles_on_land", tt.SettlesOnLand(), r.settlesOnLand},
		} {
			if f.enum != f.table {
				return fmt.Errorf("tasktype: id=%d %s mismatch: enum=%t, table=%t",
					int(tt), f.column, f.enum, f.table)
			}
		}
		delete(byID, int(tt))
	}

	for id, r := range byID {
		return fmt.Errorf("tasktype: task_types row id=%d slug=%q has no matching enum constant",
			id, r.slug)
	}

	return nil
}
