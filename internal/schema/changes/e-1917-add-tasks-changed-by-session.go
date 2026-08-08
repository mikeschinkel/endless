// E-1917: add tasks.changed_by_session — the session that last changed a task,
// so the tasks_notify_sessions trigger can skip notifying the session that made
// the change. A session told about its own edit is noise, and noise is what
// trains an agent to skim the line that matters.
//
// Nullable and additive, so schema.sql declares the post-migration shape and
// this file brings existing DBs up to it. CREATE TABLE IF NOT EXISTS no-ops on a
// populated DB, so a new column never reaches one without a change file.
//
// A `.go` change rather than `.sql` because SQLite has no ADD COLUMN IF NOT
// EXISTS: the probe below makes this a no-op on a DB already built from
// schema.sql (fresh DBs, the sandbox, and tests all have the column from the
// CREATE TABLE). The _schema_version marker gates re-runs on top of that.
//
// ORDERING (important): schema.sql's tasks_notify_sessions trigger reads
// NEW.changed_by_session, and SQLite resolves a trigger body at FIRE time, not
// CREATE time — so on a populated DB, CREATE TRIGGER succeeds and every UPDATE
// tasks then fails with "no such column" until this change is applied. Apply it
// at land (`endless worktree land` runs `endless db apply-change`) BEFORE the
// new binary becomes the deployed one via `just install`. Landing first is the
// normal order and leaves no window; installing an unlanded build against the
// real ledger is what would open one. Note E-1818 already bars a worktree
// binary pinned onto a real DB from applying schema.SQL at all, so a self-dev
// worktree cannot create the trigger on the real DB ahead of this change.
package main

import (
	"database/sql"
	"fmt"

	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		var present int
		err := tx.QueryRow(
			`SELECT count(*) FROM pragma_table_info('tasks')
			  WHERE name = 'changed_by_session'`,
		).Scan(&present)
		if err != nil {
			return fmt.Errorf("probing tasks columns: %w", err)
		}
		if present > 0 {
			return nil
		}
		if _, err := tx.Exec(
			"ALTER TABLE tasks ADD COLUMN changed_by_session INTEGER",
		); err != nil {
			return fmt.Errorf("adding tasks.changed_by_session: %w", err)
		}
		return nil
	})
}
