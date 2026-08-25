package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// Repair of the session→task bindings destroyed before ED-1560 (E-1967).
//
// `sessions.task_id` is the ownership record the `task spawn` guard consults:
// which session, if any, ever claimed this task. It is write-once now — E-1968
// removed every writer that cleared it and E-1969 installed the trigger — but
// the code that cleared it ran for months first. `task reopen` NULLed the
// binding in the same command that recorded a defect report against the landed
// work, so the sessions whose reasoning is most worth recovering are exactly the
// ones whose link was cut.
//
// The ledger is the only surviving evidence of those claims. `sessions` is
// machine-local runtime state, not a projection: the projector (projector.go)
// has cases for task and decision events only, and none for `task.claimed` or
// `task.released`. That cuts both ways and both ways are wanted here —
// `rebuild-db` will never undo this repair, and it would never have performed
// it either.
//
// Applied once, by internal/schema/changes/e-1967-restore-claim-bindings.go.
// Lives here rather than in that file because a `//go:build ignore` script is
// invisible to `go test`, and rather than in internal/monitor because reading
// the ledger is this package's job and monitor cannot import it (the dependency
// runs the other way).

// ClaimBindingRepair reports what RepairClaimBindings did. Every field is
// counted even when zero: this repair edits rows a user cares about, and silence
// would leave "nothing needed fixing" indistinguishable from "it never ran".
type ClaimBindingRepair struct {
	// Projects whose ledger was read.
	Projects int
	// Sessions whose binding was restored from a `task.claimed` entry.
	Restored int
	// Sessions skipped because the ledger names more than one distinct task for
	// them — a pre-write-once rebind, where which claim was "the" one is
	// unknowable. Guessing would invent history; the fallback is today's
	// behavior, which is not a regression.
	Ambiguous int
	// Sessions skipped because the task the ledger names no longer exists.
	// sessions.task_id carries a FK to tasks(id), so writing it would fail.
	MissingTask int
}

// RepairClaimBindings restores `sessions.task_id` for every session that has no
// binding but does have a `task.claimed` ledger entry naming it.
//
// Only NULL → value writes are performed, so the ED-1560 write-once trigger
// cannot fire: it aborts on `OLD.task_id IS NOT NULL`, and a session that still
// holds a binding is never a candidate. A session whose ledger evidence is
// ambiguous is left alone rather than guessed at.
//
// Ledgers are located per project from `projects.path`, so this works on any
// tracked project rather than only the one it was written for. A project whose
// ledger directory is missing or unreadable is skipped with an error return —
// the caller's transaction rolls back — EXCEPT for a simply-absent directory,
// which ReadAllEvents reports as no events at all.
func RepairClaimBindings(tx *sql.Tx) (ClaimBindingRepair, error) {
	var repair ClaimBindingRepair

	unbound, err := unboundSessionIDs(tx)
	if err != nil {
		return repair, err
	}
	if len(unbound) == 0 {
		return repair, nil
	}

	roots, err := projectRoots(tx)
	if err != nil {
		return repair, err
	}
	repair.Projects = len(roots)

	// session id → the distinct tasks the ledger says it claimed.
	claimed := make(map[int64]map[int64]struct{}, len(unbound))
	for _, root := range roots {
		evts, err := ReadAllEvents(root)
		if err != nil {
			return repair, fmt.Errorf("events: repair claim bindings: %w", err)
		}
		for i := range evts {
			evt := &evts[i]
			if evt.Kind != KindTaskClaimed {
				continue
			}
			var p TaskClaimedPayload
			if err := json.Unmarshal(evt.Payload, &p); err != nil {
				// A malformed payload is one unusable entry, not a reason to
				// abandon a repair the rest of the ledger still supports.
				continue
			}
			if _, ok := unbound[p.SessionID]; !ok {
				continue
			}
			taskID := mustParseInt64(evt.Entity.ID)
			if taskID == 0 {
				// An entity id that does not parse to a task. Nothing to bind.
				continue
			}
			if claimed[p.SessionID] == nil {
				claimed[p.SessionID] = make(map[int64]struct{}, 1)
			}
			claimed[p.SessionID][taskID] = struct{}{}
		}
	}

	// Sorted so a run's log output is stable and diffable.
	sessionIDs := make([]int64, 0, len(claimed))
	for id := range claimed {
		sessionIDs = append(sessionIDs, id)
	}
	sort.Slice(sessionIDs, func(i, j int) bool { return sessionIDs[i] < sessionIDs[j] })

	for _, sessionID := range sessionIDs {
		tasks := claimed[sessionID]
		if len(tasks) != 1 {
			repair.Ambiguous++
			continue
		}
		var taskID int64
		for id := range tasks {
			taskID = id
		}

		var exists int
		if err := tx.QueryRow(
			"SELECT count(*) FROM tasks WHERE id = ?", taskID,
		).Scan(&exists); err != nil {
			return repair, fmt.Errorf(
				"events: repair claim bindings: probe task %d: %w", taskID, err)
		}
		if exists == 0 {
			repair.MissingTask++
			continue
		}

		// `AND task_id IS NULL` is belt-and-braces on top of the candidate set:
		// it makes the write provably a NULL → value transition, the one the
		// write-once trigger permits, no matter what happened between the two
		// statements.
		res, err := tx.Exec(
			"UPDATE sessions SET task_id = ? WHERE id = ? AND task_id IS NULL",
			taskID, sessionID,
		)
		if err != nil {
			return repair, fmt.Errorf(
				"events: repair claim bindings: bind session %d to task %d: %w",
				sessionID, taskID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return repair, fmt.Errorf(
				"events: repair claim bindings: rows affected: %w", err)
		}
		repair.Restored += int(n)
	}

	return repair, nil
}

// unboundSessionIDs is every session with no task binding — the only rows this
// repair may write, and the filter that keeps the ledger walk from building a
// map of claims it has no use for.
func unboundSessionIDs(tx *sql.Tx) (map[int64]struct{}, error) {
	rows, err := tx.Query("SELECT id FROM sessions WHERE task_id IS NULL")
	if err != nil {
		return nil, fmt.Errorf("events: repair claim bindings: query sessions: %w", err)
	}
	defer rows.Close()

	ids := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("events: repair claim bindings: scan session id: %w", err)
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("events: repair claim bindings: iterate sessions: %w", err)
	}
	return ids, nil
}

// projectRoots is the resolved on-disk root of every registered project, in id
// order. Resolved (not stored) because `projects.path` is written home-relative
// since E-2011 and the ledger is read from the filesystem. A project whose path
// cannot be resolved is skipped rather than fatal: one unreadable registration
// must not block a repair every other project still needs.
func projectRoots(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query("SELECT path FROM projects ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("events: repair claim bindings: query projects: %w", err)
	}
	defer rows.Close()

	var roots []string
	for rows.Next() {
		var stored sql.NullString
		if err := rows.Scan(&stored); err != nil {
			return nil, fmt.Errorf("events: repair claim bindings: scan project path: %w", err)
		}
		if !stored.Valid || stored.String == "" {
			continue
		}
		resolved, err := monitor.ResolvedProjectPath(stored.String)
		if err != nil || resolved == "" {
			continue
		}
		roots = append(roots, resolved)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("events: repair claim bindings: iterate projects: %w", err)
	}
	return roots, nil
}
