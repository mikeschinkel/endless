//go:build ignore

// E-2161: restore the parent edges removal nulled, from the ledger.
//
// Until E-2161, removing a task ran `UPDATE tasks SET parent_id = NULL WHERE
// parent_id = ?` on its children (and `task import --replace` ran the bulk form
// over every child of every task the source file owned), mirroring the
// ON DELETE SET NULL the hard-delete path used. E-1929 had already stopped
// deleting the ROW; the edge kept being discarded. There is no restore verb, so
// nothing in the live database can reconstruct one of those edges.
//
// The ledger can. The nulling was a SIDE EFFECT of removal and emitted no event
// of its own, so the last parent-bearing event a task carries still names the
// parent it was filed under. This walks that back.
//
// The rule, and why it is exactly right rather than merely convenient:
//
//   - The AUTHORITY is the last event that NAMES a parent — task.created and
//     task.imported (both carry parent_id, absent meaning root), task.moved
//     (carries new_parent_id, null meaning root), and task.fields_updated only
//     when its field map actually contains parent_id.
//   - Because a null is as authoritative as an id, a task deliberately re-rooted
//     with `task move <id> --root` ends on an event that says NULL and is
//     correctly left alone. That is the distinction that makes this safe to run
//     over a whole database: "was never parented" and "was orphaned by a
//     removal" are different histories, and the ledger tells them apart.
//   - A task with no parent-bearing event at all (older ledger, imported before
//     the event existed) is left alone too. Silence is not evidence.
//
// NO EVENT IS EMITTED for a restore, and that is deliberate. The ledger already
// says the parent is set — the live database is what drifted away from it. An
// event here would make a rebuild apply the edge twice: once from the original
// task.created/moved, once from this. After this runs, the live database and a
// ledger replay AGREE about parentage, which is the whole point; `endless-go
// event validate-db` is the check that says so.
//
// Rebuild parity is the other half. A rebuild replays removal through the same
// internal/events code the live path uses, and that code no longer nulls, so a
// database rebuilt from an old ledger now keeps edges the live database had
// lost. Without this change the two would disagree on every such task; with it
// they converge.
//
// A `.go` change rather than `.sql`: the evidence lives in JSONL files on disk,
// one ledger per project, and SQLite cannot read them.
//
// Idempotent on top of the _schema_version marker: a second run finds those
// tasks no longer NULL and has nothing to select.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` (the apply-change dispatcher) names
// the file explicitly and runs it regardless.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

// maxAncestorWalk bounds the cycle guard below, mirroring the cap in
// internal/events/epic_derivation.go.
const maxAncestorWalk = 64

type tally struct {
	restored    int
	deliberate  int // the ledger's last word was "root" — left alone
	parentGone  int // the ledger names a parent whose row no longer exists
	noEvidence  int // no parent-bearing event for this task at all
	wouldCycle  int // restoring would close a parent_id loop
	ledgersRead int
}

func main() {
	runner.Run(func(tx *sql.Tx) error {
		var t tally

		projects, err := projectRoots(tx)
		if err != nil {
			return err
		}
		for _, pr := range projects {
			if err = restoreProject(tx, pr, &t); err != nil {
				return err
			}
		}

		log.Printf(
			"apply-change: e-2161: %d ledger(s) read; %d parent edge(s) restored; "+
				"%d left at root (deliberate); %d skipped (parent row gone); "+
				"%d skipped (no parent-bearing event); %d skipped (would cycle)",
			t.ledgersRead, t.restored, t.deliberate, t.parentGone, t.noEvidence, t.wouldCycle,
		)
		return nil
	})
}

type projectRef struct {
	id   int64
	name string
	root string
}

// projectRoots lists every registered project with its resolved checkout
// directory — the directory whose .endless/db-ledger/ holds that project's
// events. Reading `projects` rather than assuming one ledger is what makes this
// work on a database tracking several projects, which is the normal case for
// anyone who is not Endless itself.
func projectRoots(tx *sql.Tx) ([]projectRef, error) {
	rows, err := tx.Query("SELECT id, name, COALESCE(path, '') FROM projects ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()

	var out []projectRef
	for rows.Next() {
		var p projectRef
		var stored string
		if err = rows.Scan(&p.id, &p.name, &stored); err != nil {
			return nil, fmt.Errorf("scanning project: %w", err)
		}
		if stored == "" {
			continue
		}
		if p.root, err = monitor.ResolvedProjectPath(stored); err != nil {
			return nil, fmt.Errorf("resolving path for project %q: %w", p.name, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func restoreProject(tx *sql.Tx, pr projectRef, t *tally) error {
	evts, err := events.ReadAllEvents(pr.root)
	if err != nil {
		return fmt.Errorf("reading ledger for project %q at %s: %w", pr.name, pr.root, err)
	}
	if len(evts) == 0 {
		// No ledger on this machine (a project registered from a checkout that
		// is not here). Nothing to reconstruct from, and nothing is wrong.
		return nil
	}
	t.ledgersRead++

	lastParent := lastParentByTask(evts)

	orphans, err := nullParentedTasks(tx, pr.id)
	if err != nil {
		return err
	}

	for _, taskID := range orphans {
		parent, seen := lastParent[taskID]
		switch {
		case !seen:
			t.noEvidence++
			continue
		case parent == nil:
			t.deliberate++
			continue
		}

		exists, err := taskExists(tx, *parent)
		if err != nil {
			return err
		}
		if !exists {
			t.parentGone++
			continue
		}

		cycles, err := wouldCycle(tx, taskID, *parent)
		if err != nil {
			return err
		}
		if cycles {
			// The write-time guards (ValidateNoParentCycle) never ran on this
			// restore, so it checks for itself rather than trusting that a
			// history assembled across many moves still composes into a tree.
			t.wouldCycle++
			continue
		}

		if _, err = tx.Exec(
			"UPDATE tasks SET parent_id = ? WHERE id = ? AND parent_id IS NULL",
			*parent, taskID,
		); err != nil {
			return fmt.Errorf("restoring parent of task %d: %w", taskID, err)
		}
		t.restored++
	}
	return nil
}

// lastParentByTask reduces the ledger to "what did the last parent-bearing event
// say", per task. A present key with a nil value means the ledger's final word
// was "this task has no parent"; an absent key means no event ever named one.
//
// events.ReadAllEvents returns the segments sorted by kairos timestamp, and a
// kairos sort is a causal sort, so a plain forward pass ends on the latest word.
func lastParentByTask(evts []events.Event) map[int64]*int64 {
	last := make(map[int64]*int64)

	for _, evt := range evts {
		if evt.Entity.Type != events.EntityTask {
			continue
		}
		var taskID int64
		if _, err := fmt.Sscanf(evt.Entity.ID, "%d", &taskID); err != nil || taskID == 0 {
			continue
		}

		switch evt.Kind {
		case events.KindTaskCreated:
			var p events.TaskCreatedPayload
			if json.Unmarshal(evt.Payload, &p) == nil {
				last[taskID] = p.ParentID
			}
		case events.KindTaskImported:
			var p events.TaskImportedPayload
			if json.Unmarshal(evt.Payload, &p) == nil {
				last[taskID] = p.ParentID
			}
		case events.KindTaskMoved:
			var p events.TaskMovedPayload
			if json.Unmarshal(evt.Payload, &p) == nil {
				last[taskID] = p.NewParentID
			}
		case events.KindTaskFieldsUpdated:
			var p events.TaskFieldsUpdatedPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			raw, ok := p.Fields["parent_id"]
			if !ok {
				// A fields update that did not touch parent_id says nothing
				// about it, and must not overwrite what an earlier event said.
				continue
			}
			last[taskID] = parentFromField(raw)
		}
	}
	return last
}

// parentFromField reads the parent_id a task.fields_updated payload carries.
// JSON numbers arrive as float64; null arrives as nil; the executor treats 0 as
// "make root" (PARENT_NONE on the Python side), so this does too.
func parentFromField(raw any) *int64 {
	v, ok := raw.(float64)
	if !ok || v <= 0 {
		return nil
	}
	id := int64(v)
	return &id
}

// nullParentedTasks lists the project's tasks that currently have no parent —
// the only rows this change may touch. Removed rows are included deliberately:
// a removed child's edge is exactly the association the non-cascade nulling
// destroyed while protecting no render at all.
func nullParentedTasks(tx *sql.Tx, projectID int64) ([]int64, error) {
	rows, err := tx.Query(
		"SELECT id FROM tasks WHERE project_id = ? AND parent_id IS NULL ORDER BY id",
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing parentless tasks: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning parentless task: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func taskExists(tx *sql.Tx, id int64) (bool, error) {
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE id = ?", id).Scan(&n); err != nil {
		return false, fmt.Errorf("probing task %d: %w", id, err)
	}
	return n > 0, nil
}

// wouldCycle reports whether making parentID the parent of taskID would close a
// loop — i.e. whether taskID is already on parentID's ancestor chain.
func wouldCycle(tx *sql.Tx, taskID, parentID int64) (bool, error) {
	current := parentID
	for range maxAncestorWalk {
		if current == taskID {
			return true, nil
		}
		var next sql.NullInt64
		err := tx.QueryRow("SELECT parent_id FROM tasks WHERE id = ?", current).Scan(&next)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("walking ancestors of %d: %w", current, err)
		}
		if !next.Valid {
			return false, nil
		}
		current = next.Int64
	}
	// The chain is already malformed. Refusing to add to it is the safe answer.
	return true, nil
}
