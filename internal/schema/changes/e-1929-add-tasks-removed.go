//go:build ignore

// E-1929: add tasks.removed, the live_tasks read view, and its index —
// implementing ED-1547 for tasks. `task remove` stops issuing a DELETE and marks
// the row removed = 1, so a task id can never be re-minted and the FK-free rows
// that deliberately outlive their task (session_tasks, session_notices,
// task_landings) can never resurrect against unrelated work.
//
// Additive, so schema.sql declares the post-migration shape (the column, the
// view, the index) and this file brings existing DBs up to it. CREATE TABLE IF
// NOT EXISTS no-ops on a populated DB, so a new column never reaches one without
// a change file.
//
// A `.go` change rather than `.sql` because SQLite has no ADD COLUMN IF NOT
// EXISTS: the probe below makes this a no-op on a DB already built from
// schema.sql (fresh DBs, the sandbox, and tests all have the column from the
// CREATE TABLE), matching e-1917's precedent for the same operation on the same
// table. A plain `ALTER TABLE tasks ADD COLUMN` in a .sql file would hard-error
// there instead. The _schema_version marker gates re-runs on top of that.
//
// ORDERING (important): schema.sql's live_tasks view reads tasks.removed, and
// SQLite resolves a view body at PREPARE time, not CREATE time — so on a
// populated DB, CREATE VIEW succeeds and every read through live_tasks then
// fails with "no such column: removed" until this change is applied. Apply it at
// land (`endless worktree land` runs `endless db apply-change`) BEFORE the new
// binary becomes the deployed one via `just install`. Landing first is the
// normal order and leaves no window; installing an unlanded build against the
// real ledger is what would open one. E-1818 already bars a worktree binary
// pinned onto a real DB from applying schema.SQL at all, so a self-dev worktree
// cannot create the view on the real DB ahead of this change.
//
// FK actions that stop firing once removal is an UPDATE, and what replaces them:
//   - sessions.active_task_id / session_statuses.active_task_id were ON DELETE
//     SET NULL. The removal path in internal/events/executor.go now nulls them
//     explicitly — a live session pointing at a removed task is a lie.
//   - task_landings.task_id was ON DELETE CASCADE and now simply will not fire.
//     That is the intended outcome, not an oversight: landing history is audit
//     data, and the retained task row is there to explain it. Do not "fix" it.
//
// The session_tasks repair below is the one-shot data half of this change
// (absorbed E-1932). It is scoped by construction, not by count — see its
// comment.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` names the file explicitly and so
// runs it regardless.
package main

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		var present int
		err := tx.QueryRow(
			`SELECT count(*) FROM pragma_table_info('tasks')
			  WHERE name = 'removed'`,
		).Scan(&present)
		if err != nil {
			return fmt.Errorf("probing tasks columns: %w", err)
		}
		if present == 0 {
			if _, err = tx.Exec(
				"ALTER TABLE tasks ADD COLUMN removed INTEGER NOT NULL DEFAULT 0",
			); err != nil {
				return fmt.Errorf("adding tasks.removed: %w", err)
			}
		}

		if _, err = tx.Exec(
			"CREATE VIEW IF NOT EXISTS live_tasks AS SELECT * FROM tasks WHERE removed = 0",
		); err != nil {
			return fmt.Errorf("creating live_tasks view: %w", err)
		}

		if _, err = tx.Exec(
			"CREATE INDEX IF NOT EXISTS idx_tasks_removed ON tasks(removed)",
		); err != nil {
			return fmt.Errorf("creating idx_tasks_removed: %w", err)
		}

		// One-shot repair of id reuse that already happened (absorbed E-1932).
		//
		// Before this change a removed task freed its id, and the next task
		// allocated took it. session_tasks has no FK, so the old occupant's touch
		// rows stayed and silently reattached to unrelated work — `task show` then
		// reported a session as having touched a task it never saw.
		//
		// The `st.created_at < t.created_at` test is a proof, not a heuristic: a
		// session cannot have touched a task before that task existed, so any such
		// row provably belongs to a previous occupant of the id. Rows whose task id
		// is simply gone are left alone — that is what "outlives its task" means,
		// and they are already invisible through live_tasks.
		//
		// Only session_tasks is swept. All four FK-free consumers were re-measured
		// against the real ledger immediately before landing: 15 reattached rows in
		// session_tasks (touches minutes-to-an-hour ahead of their task's creation,
		// i.e. the previous occupant of a reused id), and ZERO in session_notices,
		// session_hidden_tasks and task_landings. The same timestamp proof would
		// not hold for the latter two anyway — a notice and a landing are immutable
		// records of an event, not assertions about the task's current identity.
		res, err := tx.Exec(
			`DELETE FROM session_tasks
			  WHERE id IN (
			    SELECT st.id FROM session_tasks st
			      JOIN tasks t ON t.id = st.task_id
			     WHERE st.created_at < t.created_at
			  )`,
		)
		if err != nil {
			return fmt.Errorf("repairing reattached session_tasks rows: %w", err)
		}
		if n, rErr := res.RowsAffected(); rErr == nil && n > 0 {
			log.Printf("e-1929: removed %d session_tasks row(s) that predated their task", n)
		}

		return nil
	})
}
