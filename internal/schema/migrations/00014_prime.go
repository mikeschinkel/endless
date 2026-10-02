package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// prime is migration 14 (E-1994): the schema half of starting a task's session
// ahead of need.
//
//   - tasks.prime_requested — set by the executor when a plan is attached
//     (its unplanned→submitted inference), read by the `prime` job, never
//     cleared: the job stops asking for a task once any session has bound to
//     it.
//   - tasks_notify_sessions stops watching description. E-1993 made the plan
//     the spec and the description only what the task is, so the
//     changed-since-you-read-it notice follows the plan (through the
//     task_content triggers, unchanged) and not the description.
//
// A .go step for 00011's reason — schema.sql already declares the column, and
// SQLite has no ADD COLUMN IF NOT EXISTS, so it is probed — and for 00008's: the
// trigger body is what changes, so it is typed out here, frozen, and must stay
// identical modulo whitespace to schema.sql's (TestMigrate_MatchesSchemaSQL).
//
// No Down: a ledger rebuild recomputes the column, and the old trigger told
// sessions about edits that no longer specify anything.
func addPrime() *goose.Migration {
	return goose.NewGoMigration(14, &goose.GoFunc{RunTx: addPrimeUp}, nil)
}

// tasksNotifySessionsAt14 is the notice trigger as of this migration. Frozen: a
// later change to the trigger is a later migration, not an edit here.
const tasksNotifySessionsAt14 = `CREATE TRIGGER tasks_notify_sessions AFTER UPDATE ON tasks
WHEN OLD.status        IS NOT NEW.status
  OR OLD.phase         IS NOT NEW.phase
  OR OLD.complexity_id IS NOT NEW.complexity_id
  OR OLD.risk_id       IS NOT NEW.risk_id
BEGIN
    INSERT INTO session_notices
        (session_id, task_id, changes, changed_at, changed_by_session)
    SELECT st.session_id,
           NEW.id,
           (SELECT json_group_object(f, json(v)) FROM (
                SELECT 'status' AS f,
                       json_object('before', OLD.status, 'after', NEW.status) AS v
                 WHERE OLD.status IS NOT NEW.status
                UNION ALL
                SELECT 'phase',
                       json_object('before', OLD.phase, 'after', NEW.phase)
                 WHERE OLD.phase IS NOT NEW.phase
                UNION ALL
                SELECT 'complexity',
                       json_object(
                           'before', (SELECT slug FROM complexity_levels WHERE id = OLD.complexity_id),
                           'after',  (SELECT slug FROM complexity_levels WHERE id = NEW.complexity_id))
                 WHERE OLD.complexity_id IS NOT NEW.complexity_id
                UNION ALL
                SELECT 'risk',
                       json_object(
                           'before', (SELECT slug FROM risk_levels WHERE id = OLD.risk_id),
                           'after',  (SELECT slug FROM risk_levels WHERE id = NEW.risk_id))
                 WHERE OLD.risk_id IS NOT NEW.risk_id
           )),
           strftime('%Y-%m-%dT%H:%M:%S', 'now'),
           NEW.changed_by_session
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
     WHERE st.task_id = NEW.id
       AND st.session_id IS NOT NEW.changed_by_session
       AND s.state != 'ended';
END`

func addPrimeUp(ctx context.Context, tx *sql.Tx) error {
	present, err := columnExists(ctx, tx, "tasks", "prime_requested")
	if err != nil {
		return err
	}
	if !present {
		if _, err = tx.ExecContext(ctx,
			`ALTER TABLE tasks ADD COLUMN prime_requested INTEGER NOT NULL DEFAULT 0`,
		); err != nil {
			return fmt.Errorf("adding tasks.prime_requested: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS tasks_notify_sessions`); err != nil {
		return fmt.Errorf("dropping tasks_notify_sessions: %w", err)
	}
	if _, err = tx.ExecContext(ctx, tasksNotifySessionsAt14); err != nil {
		return fmt.Errorf("recreating tasks_notify_sessions: %w", err)
	}
	return nil
}
