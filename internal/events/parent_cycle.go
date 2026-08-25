package events

import (
	"database/sql"
	"fmt"
)

// maxAncestorWalk bounds the ancestor walk so a database that already
// contains a cycle cannot spin this validator forever. It is a backstop, not
// a policy: the visited set below detects a real loop long before the depth
// runs out, and no honest task tree comes close to this depth.
const maxAncestorWalk = 10000

// ValidateNoParentCycle rejects a parent assignment that would make a task its
// own ancestor. Callers pass the task being written and the effective parent
// the write would produce, so one check covers both paths that set
// tasks.parent_id — task.moved and task.fields_updated — exactly as
// ValidateMaybeParentless already does for the maybe-phase rule (E-2067).
//
// A nil parentID (root task) is always allowed: detaching can never create a
// cycle, which is also what makes `task move <id> --root` the escape hatch out
// of one.
//
// The walk climbs from the target parent toward the root. Three outcomes:
// reaching the root is legal; reaching taskID means the write would close a
// loop; revisiting a node means the chain ABOVE the target parent is already
// corrupt, which is refused too — attaching under it would give the task an
// ancestor chain that every recursive CTE over the task tree walks forever.
//
// A parent row that cannot be read ends the walk without error. That is
// deliberate: a missing or unreadable ancestor is not evidence of a cycle, and
// referential integrity is the FK's job, not this validator's.
func ValidateNoParentCycle(db dbQuerier, taskID int64, parentID *int64) error {
	if parentID == nil {
		return nil
	}
	if *parentID == taskID {
		return fmt.Errorf("events: circular reference: task %d cannot be its own parent", taskID)
	}

	seen := map[int64]bool{*parentID: true}
	current := *parentID
	for range maxAncestorWalk {
		var next sql.NullInt64
		if err := db.QueryRow("SELECT parent_id FROM tasks WHERE id = ?", current).Scan(&next); err != nil {
			return nil
		}
		if !next.Valid {
			return nil
		}
		if next.Int64 == taskID {
			return fmt.Errorf("events: circular reference: task %d is an ancestor of target parent %d", taskID, *parentID)
		}
		if seen[next.Int64] {
			return fmt.Errorf("events: circular reference: the ancestor chain above task %d already contains a cycle at task %d; detach it first (task move <id> --root)", *parentID, next.Int64)
		}
		seen[next.Int64] = true
		current = next.Int64
	}
	return fmt.Errorf("events: ancestor chain above task %d exceeds %d levels; refusing to treat it as acyclic", *parentID, maxAncestorWalk)
}
