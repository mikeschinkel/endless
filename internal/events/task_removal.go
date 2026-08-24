package events

import (
	"fmt"
	"strings"
)

// Task removal, per ED-1547 (E-1929).
//
// Removing a task no longer DELETEs its row: it sets removed = 1 and leaves the
// row in place. The id is therefore never re-minted — the allocator's
// `SELECT COALESCE(MAX(id), 0) + 1 FROM tasks` keeps counting past it — so the
// FK-free rows that deliberately outlive their task (session_tasks,
// session_notices, task_landings) can never resurrect against unrelated work.
// Reads filter removed rows out through the live_tasks view.
//
// This file is the single implementation of that removal, called from BOTH the
// executor (the live write path) and the projector (the ledger-replay rebuild
// path). Sharing one function is the point: if the projector still replayed
// removal as a real DELETE while the executor marked removed = 1, a rebuild
// would silently re-free every removed id — the exact failure ED-1547 names, on
// a path nobody would think to test. Two copies can drift; one cannot.
//
// The two halves of every query below are deliberately asymmetric, and this is
// the subtle part of the change:
//
//   - ENUMERATION reads live_tasks. "What does this removal cover?" means the
//     rows that would still have existed under hard delete, so an already-removed
//     task is not re-removed and a repeated bulk clear is the no-op it is today.
//   - MUTATION names tasks. The view is not writable, and the removal path is one
//     of the three places that must see and set what the view hides.

// removeTaskTree marks taskID removed, and under cascade every descendant with
// it. It returns the ids actually removed, in walk order with the root first —
// the caller needs them to record per-task session touches.
//
// Non-cascade re-roots the removed task's children to NULL, exactly as the
// hard-delete path did via `ON DELETE SET NULL`. Retention makes that MORE
// important, not less: a child left pointing at a row the view hides would hang
// off an invisible parent and drop out of every tree render.
func removeTaskTree(db dbQuerier, taskID int64, cascade bool) ([]int64, error) {
	var (
		ids []int64
		err error
	)
	if cascade {
		ids, err = queryTaskIDs(db,
			`WITH RECURSIVE tree(id) AS (
				SELECT id FROM live_tasks WHERE id = ?
				UNION ALL
				SELECT t.id FROM live_tasks t JOIN tree ON t.parent_id = tree.id
			) SELECT id FROM tree`,
			taskID,
		)
		if err != nil {
			return nil, fmt.Errorf("events: enumerate cascade removal tree: %w", err)
		}
	} else {
		ids, err = queryTaskIDs(db, "SELECT id FROM live_tasks WHERE id = ?", taskID)
		if err != nil {
			return nil, fmt.Errorf("events: enumerate removal target: %w", err)
		}
		if _, err = db.Exec(
			"UPDATE tasks SET parent_id = NULL WHERE parent_id = ?", taskID,
		); err != nil {
			return nil, fmt.Errorf("events: re-root children of removed task: %w", err)
		}
	}

	if err = applyTaskRemoval(db, ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// removeTasksBySourceFile is the bulk-clear (`task import --replace`) removal:
// every task the given source file owns in the given project.
//
// It retains too, rather than hard-deleting because the rows are "just imported
// data" — E-1915's reasoning applies. One rule with no exemption beats two rules
// with a judgment call at the boundary, and a second orphaning path is exactly
// what ED-1547 exists to close.
func removeTasksBySourceFile(db dbQuerier, projectID int64, sourceFile string) ([]int64, error) {
	ids, err := queryTaskIDs(db,
		"SELECT id FROM live_tasks WHERE project_id = ? AND source_file = ?",
		projectID, sourceFile,
	)
	if err != nil {
		return nil, fmt.Errorf("events: enumerate bulk_cleared tasks: %w", err)
	}

	if _, err = db.Exec(
		`UPDATE tasks SET parent_id = NULL WHERE parent_id IN (
			SELECT id FROM live_tasks WHERE project_id = ? AND source_file = ?
		)`, projectID, sourceFile,
	); err != nil {
		return nil, fmt.Errorf("events: re-root children of bulk-cleared tasks: %w", err)
	}

	if err = applyTaskRemoval(db, ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// applyTaskRemoval marks the given ids removed and performs the cleanups the
// FOREIGN KEY actions used to do for free. An UPDATE fires no FK action, so
// everything a DELETE cascaded has to be spelled out here — or deliberately not.
func applyTaskRemoval(db dbQuerier, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	in, args := idInClause(ids)

	if _, err := db.Exec(
		"UPDATE tasks SET removed = 1 WHERE id IN "+in, args...,
	); err != nil {
		return fmt.Errorf("events: mark tasks removed: %w", err)
	}

	// sessions.task_id is NOT cleared here. It was ON DELETE SET NULL under hard
	// delete, and E-1929 carried that forward as an explicit UPDATE — but ED-1560
	// makes the column write-once (set at claim, never cleared, never repointed),
	// enforced by the sessions_task_id_write_once trigger, so this clear would
	// now abort the whole removal. Nor is it needed: retention leaves the task
	// row in place, so a session that claimed a removed task still resolves to a
	// real row that reads `removed`. Do not reinstate it.
	//
	// session_statuses.task_id is different and still cleared: a status snapshot
	// is not an ownership record, carries no trigger, and its FK was likewise
	// ON DELETE SET NULL.
	if _, err := db.Exec(
		"UPDATE session_statuses SET task_id = NULL WHERE task_id IN "+in, args...,
	); err != nil {
		return fmt.Errorf("events: clear session_statuses.task_id: %w", err)
	}

	// Undelivered mail about a removed task is dead: filtering it on read would
	// leave it queued permanently, so it is dropped outright. Delivered history
	// (notified = 1) is untouched — it records what a session was actually shown.
	//
	// Not an exception to ED-1547: a notice's own id is an internal row handle
	// nobody stores or types, which is precisely the class that stays a hard
	// delete.
	if _, err := db.Exec(
		"DELETE FROM session_notices WHERE notified = 0 AND task_id IN "+in, args...,
	); err != nil {
		return fmt.Errorf("events: drop pending notices for removed tasks: %w", err)
	}

	// task_landings was ON DELETE CASCADE and now simply will not fire. That is
	// the intended outcome, not an oversight: landing history is audit data, and
	// the retained task row is there to explain it. Do not "fix" this.

	return nil
}

// queryTaskIDs runs a query whose single column is a task id.
func queryTaskIDs(db dbQuerier, query string, args ...any) ([]int64, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// idInClause renders "(?, ?, …)" plus the matching args for an IN predicate.
func idInClause(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}
