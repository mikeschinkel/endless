package events

import (
	"database/sql"
	"fmt"

	"github.com/mikeschinkel/endless/internal/tasktype"
)

// Epic status auto-derivation (E-1541). An epic's status is a pure function of
// its children's statuses, except when the epic itself sits in a sticky-override
// status. recomputeEpicStatus is called from the executor entry points whose
// mutations can change a parent epic's child set or a child's status. It fires
// inline inside the triggering mutation's SQL transaction, so the derived state
// commits atomically with the change that caused it.
//
// The projector does NOT call recomputeEpicStatus. The live path records each
// derivation as an epic.status_derived ledger entry (via the DerivedEmitter),
// and the projector replays those entries through replayEpicStatusDerived. This
// keeps projection(ledger) == live DB so validate-db stays meaningful. See the
// E-1541 plan §0 for the rationale.

// DerivedEmitter records an epic.status_derived event for one epic whose status
// the derivation rule just changed. It is threaded from eventcmd.run on the live
// path, where it appends the event to the current ledger segment and commits it.
// It is nil in the projector and in unit tests, where recompute updates the DB
// only.
type DerivedEmitter func(epicID int64, oldStatus, newStatus string) error

// maxAncestorDepth caps the WITH RECURSIVE ancestor walk. parent_id cycles are
// already prevented at write time by execTaskMoved's ancestor-loop check; the
// cap is cheap insurance against a malformed tree (E-1541 §6).
const maxAncestorDepth = 32

// stickyOverrideStatuses block derivation: while an epic sits in one of these,
// recompute reads its state and does nothing. The override must be cleared
// (manually set to a derivable status) before derivation resumes (E-1541 §1).
var stickyOverrideStatuses = map[string]bool{
	"revisit":  true,
	"declined": true,
	"obsolete": true,
	"blocked":  true,
}

// terminalChildStatuses are the statuses that count as "done" when deciding
// whether all of an epic's children are terminal (E-1541 §1).
var terminalChildStatuses = map[string]bool{
	"completed": true,
	"confirmed": true,
	"assumed":   true,
	"declined":  true,
	"obsolete":  true,
}

// recomputeEpicStatus re-derives the status of every epic at or above each
// parentID — the parents whose child set or whose child's status just changed —
// and applies any change inside the caller's transaction. Pass the immediate
// parent(s) of the mutated task: a status change passes the changed task's
// parent; a move passes both the old and new parents; a delete passes the
// deleted task's former parent. The changed task itself is never re-derived, so
// an explicit status set on it (e.g. a cascade confirm) is preserved.
//
// A single seen-set is shared across all parentIDs to dedupe common ancestors
// and to guard cycles alongside the depth cap. emit may be nil (projector /
// tests), in which case the DB is updated but no ledger entry is recorded.
func recomputeEpicStatus(db dbQuerier, emit DerivedEmitter, parentIDs ...int64) error {
	seen := make(map[int64]bool)
	for _, parentID := range parentIDs {
		epicIDs, err := epicAncestorsInclusive(db, parentID)
		if err != nil {
			return err
		}
		// Nearest-first order (depth ASC) matters: a lower epic's new status
		// must be visible when a higher epic reads its children.
		for _, epicID := range epicIDs {
			if seen[epicID] {
				continue
			}
			seen[epicID] = true
			if err := deriveOneEpic(db, emit, epicID); err != nil {
				return err
			}
		}
	}
	return nil
}

// epicAncestorsInclusive returns the ids of every epic on the path from startID
// up to the root, including startID itself when it is an epic, ordered
// nearest-first. The depth cap bounds a malformed (cyclic) parent chain.
func epicAncestorsInclusive(db dbQuerier, startID int64) ([]int64, error) {
	rows, err := db.Query(
		`WITH RECURSIVE ancestors(id, parent_id, type_id, depth) AS (
			SELECT id, parent_id, type_id, 0 FROM tasks WHERE id = ?
			UNION ALL
			SELECT t.id, t.parent_id, t.type_id, a.depth + 1
			FROM tasks t JOIN ancestors a ON t.id = a.parent_id
			WHERE a.depth < ?
		)
		SELECT id FROM ancestors WHERE type_id = ? ORDER BY depth ASC`,
		startID, maxAncestorDepth, int(tasktype.TaskTypeEpic),
	)
	if err != nil {
		return nil, fmt.Errorf("events: walk epic ancestors of %d: %w", startID, err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("events: scan epic ancestor: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// deriveOneEpic recomputes a single epic's status from its direct children and
// applies the change (status + completed_at) when it differs from the current
// value. Sticky-override statuses short-circuit. On an actual change it invokes
// emit (when non-nil) to record the derivation.
func deriveOneEpic(db dbQuerier, emit DerivedEmitter, epicID int64) error {
	var current string
	if err := db.QueryRow(
		"SELECT status FROM tasks WHERE id = ?", epicID,
	).Scan(&current); err != nil {
		return fmt.Errorf("events: read epic %d status: %w", epicID, err)
	}
	if stickyOverrideStatuses[current] {
		return nil
	}

	target, ok, err := deriveTargetStatus(db, epicID)
	if err != nil {
		return err
	}
	if !ok || target == current {
		return nil
	}

	var completedAt any
	if target == "completed" {
		completedAt = now()
	}
	if _, err := db.Exec(
		"UPDATE tasks SET status = ?, completed_at = ? WHERE id = ?",
		target, completedAt, epicID,
	); err != nil {
		return fmt.Errorf("events: derive epic %d status: %w", epicID, err)
	}

	if emit != nil {
		if err := emit(epicID, current, target); err != nil {
			return fmt.Errorf("events: emit derived event for epic %d: %w", epicID, err)
		}
	}
	return nil
}

// deriveTargetStatus computes the rule from E-1541 §1 against an epic's direct
// children. The bool is false (and the string empty) when no derivation applies:
// the epic has zero children, or children exist but none fall in a derivable
// bucket (e.g. all in verify/blocked, which is neither in_progress/ready/
// needs_plan nor fully terminal). In that case the epic is left unchanged.
func deriveTargetStatus(db dbQuerier, epicID int64) (string, bool, error) {
	rows, err := db.Query("SELECT status FROM tasks WHERE parent_id = ?", epicID)
	if err != nil {
		return "", false, fmt.Errorf("events: read children of epic %d: %w", epicID, err)
	}
	defer rows.Close()

	var (
		hasChild      bool
		anyInProgress bool
		anyReady      bool
		anyNeedsPlan  bool
		allTerminal   = true
	)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", false, fmt.Errorf("events: scan child status: %w", err)
		}
		hasChild = true
		switch s {
		case "in_progress":
			anyInProgress = true
		case "ready":
			anyReady = true
		case "needs_plan":
			anyNeedsPlan = true
		}
		if !terminalChildStatuses[s] {
			allTerminal = false
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}

	switch {
	case !hasChild:
		return "", false, nil
	case anyInProgress:
		return "in_progress", true, nil
	case anyReady:
		return "ready", true, nil
	case anyNeedsPlan:
		return "needs_plan", true, nil
	case allTerminal:
		return "completed", true, nil
	default:
		return "", false, nil
	}
}

// taskParentID reads a task's parent_id. The second result is false when the
// task has no parent (or the row is gone). Used by the executor entry points to
// resolve the parent chain to recompute after a child mutation.
func taskParentID(db dbQuerier, taskID int64) (int64, bool, error) {
	var pid sql.NullInt64
	if err := db.QueryRow(
		"SELECT parent_id FROM tasks WHERE id = ?", taskID,
	).Scan(&pid); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("events: read parent of task %d: %w", taskID, err)
	}
	if !pid.Valid {
		return 0, false, nil
	}
	return pid.Int64, true, nil
}
