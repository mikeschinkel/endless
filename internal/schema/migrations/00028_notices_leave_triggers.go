// E-2422: drop the five session-notice triggers and tasks.changed_by_session.
//
// tasks_notify_sessions, task_content_notify_insert/update/delete and
// task_landings_notify_sessions fired on project tables and wrote the machine
// table session_notices, reading session_tasks and sessions. A SQLite trigger
// cannot reference a table in another attached database, so none of them could
// survive tasks moving to a per-project file (E-2215). The Go executor writes
// the notices now, after the task write commits
// (internal/events/session_notices.go), keeping every rule the triggers
// carried.
//
// tasks.changed_by_session existed only for those triggers: the executor
// stamped the acting session on the row so a trigger, which cannot read the
// event, could suppress that session's own notice. The executor reads the actor
// off the event itself now, so the column has no reader and goes too — it would
// otherwise carry a machine-local session id into every project's database.
//
// The triggers go first: ALTER TABLE ... DROP COLUMN refuses while a trigger
// names the column, and all four task-side triggers do.
//
// A .go step for the reason the package header gives: schema.sql no longer
// declares the column, and SQLite has no DROP COLUMN IF EXISTS, so it is probed
// and dropped only when present. The triggers need no probe; DROP TRIGGER IF
// EXISTS says it.
//
// ORDERING, the hazard 00003 documents: once this applies, an endless-go binary
// older than it still stamps tasks.changed_by_session and fails with "no such
// column". `just land` rebuilds the binaries immediately after main advances,
// so the window is inside one land.
//
// No Down: goose never rolls this set back in production, and the column held
// nothing a rebuild could not do without.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func noticesLeaveTriggers() *goose.Migration {
	return goose.NewGoMigration(28, &goose.GoFunc{RunTx: noticesLeaveTriggersUp}, nil)
}

// noticeTriggers are the triggers E-2422 replaced with executor code.
var noticeTriggers = []string{
	"tasks_notify_sessions",
	"task_content_notify_insert",
	"task_content_notify_update",
	"task_content_notify_delete",
	"task_landings_notify_sessions",
}

func noticesLeaveTriggersUp(ctx context.Context, tx *sql.Tx) error {
	for _, name := range noticeTriggers {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+name); err != nil {
			return fmt.Errorf("dropping %s: %w", name, err)
		}
	}
	present, err := columnExists(ctx, tx, "tasks", "changed_by_session")
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE tasks DROP COLUMN changed_by_session`); err != nil {
		return fmt.Errorf("dropping tasks.changed_by_session: %w", err)
	}
	return nil
}

// noticeTriggerCanFire reports whether tasks still has the column the frozen
// tasks_notify_sessions bodies in 00008 and 00014 read, which is whether those
// steps may recreate the trigger.
//
// A database built from a schema.sql at or past E-2422 reaches 00008 and 00014
// without tasks.changed_by_session. SQLite resolves a trigger body at FIRE
// time, so recreating the trigger there succeeds and then fails the next
// UPDATE tasks — 00010's, on a replay — with "no such column". Skipping it is
// the shape 00028 would leave anyway: it drops the trigger.
func noticeTriggerCanFire(ctx context.Context, tx *sql.Tx) (bool, error) {
	return columnExists(ctx, tx, "tasks", "changed_by_session")
}
