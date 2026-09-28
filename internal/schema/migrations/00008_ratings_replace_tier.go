package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// ratingsReplaceTier is migration 8 (E-1813, implementing ED-1538/ED-1539):
// replace the bare tasks.tier INTEGER with two nullable FK columns,
// tasks.complexity_id and tasks.risk_id, pointing at two new mirror tables,
// complexity_levels and risk_levels. The rows are seeded by seeds.sql, which
// Seed() runs after every migration, exactly as for the other enum mirrors.
//
// # THE TIER VALUES ARE DROPPED, NOT MAPPED
//
// About 98% of rows held NULL or 0 ("n/a"); 28 held a meaningful tier. Those are
// deliberately not carried onto complexity: a mapped value would read as a
// rating the user had ratified at approve, and nobody did. Unrated is the
// honest state, and an unrated task is refused at approve until someone rates
// it, which is where these 28 get their real ratings.
//
// WHY THIS STEP IS `.go`
//
// For the reason 00003 gives: SQLite has no DROP COLUMN IF EXISTS or ADD COLUMN
// IF NOT EXISTS, and a database schema.sql built reaches this step already at
// the target shape. Each column is probed and skipped when already there.
//
// # THE NOTICE TRIGGER IS REBUILT
//
// ALTER TABLE ... DROP COLUMN refuses while a trigger names the column, and
// tasks_notify_sessions names tier. It is dropped first and recreated last, now
// watching the two rating columns instead. Its body is typed out here rather
// than read back from sqlite_master (00007's technique) because the body is
// what changes; it must stay identical, modulo whitespace, to schema.sql's,
// which TestMigrate_MatchesSchemaSQL asserts.
//
// RunTx, so the whole step lands or none of it does.
//
// ORDERING, the hazard 00003 documents: once this applies, an endless-go binary
// older than it still names tasks.tier and fails with "no such column". `just
// land` rebuilds the binaries immediately after main advances, so the window is
// inside one land.
//
// No Down: the tier values are dropped on purpose (see above), so a Down could
// restore the column but not what it held.
func ratingsReplaceTier() *goose.Migration {
	return goose.NewGoMigration(8, &goose.GoFunc{RunTx: ratingsReplaceTierUp}, nil)
}

// ratingLevelTables are created verbatim as schema.sql declares them.
var ratingLevelTables = []string{
	`CREATE TABLE IF NOT EXISTS complexity_levels (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS risk_levels (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
)`,
}

// ratingColumns are added in this order, which is the order schema.sql declares.
var ratingColumns = []struct{ name, ddl string }{
	{"complexity_id", "complexity_id INTEGER REFERENCES complexity_levels(id)"},
	{"risk_id", "risk_id INTEGER REFERENCES risk_levels(id)"},
}

// tasksNotifySessionsAt8 is the notice trigger as of this migration. Frozen: a
// later change to the trigger is a later migration, not an edit here.
const tasksNotifySessionsAt8 = `CREATE TRIGGER tasks_notify_sessions AFTER UPDATE ON tasks
WHEN OLD.status        IS NOT NEW.status
  OR OLD.phase         IS NOT NEW.phase
  OR OLD.complexity_id IS NOT NEW.complexity_id
  OR OLD.risk_id       IS NOT NEW.risk_id
  OR OLD.description   IS NOT NEW.description
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
                UNION ALL
                SELECT 'description',
                       json_object(
                           'before', CASE WHEN OLD.description IS NULL THEN NULL
                                          WHEN OLD.description = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.description IS NULL THEN NULL
                                          WHEN NEW.description = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.description IS NOT NEW.description
           )),
           strftime('%Y-%m-%dT%H:%M:%S', 'now'),
           NEW.changed_by_session
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
     WHERE st.task_id = NEW.id
       AND st.session_id IS NOT NEW.changed_by_session
       AND s.state != 'ended';
END`

func ratingsReplaceTierUp(ctx context.Context, tx *sql.Tx) error {
	for _, ddl := range ratingLevelTables {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("creating a rating-level table: %w", err)
		}
	}

	// The trigger goes first: it names tier, and DROP COLUMN refuses while it
	// does. It is recreated unconditionally below, so a schema.sql-built
	// database ends with the same trigger it started with.
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS tasks_notify_sessions`); err != nil {
		return fmt.Errorf("dropping tasks_notify_sessions: %w", err)
	}

	hasTier, err := columnExists(ctx, tx, "tasks", "tier")
	if err != nil {
		return err
	}
	if hasTier {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE tasks DROP COLUMN tier`); err != nil {
			return fmt.Errorf("dropping tasks.tier: %w", err)
		}
	}

	for _, c := range ratingColumns {
		present, err := columnExists(ctx, tx, "tasks", c.name)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if _, err = tx.ExecContext(ctx, `ALTER TABLE tasks ADD COLUMN `+c.ddl); err != nil {
			return fmt.Errorf("adding tasks.%s: %w", c.name, err)
		}
	}

	if _, err = tx.ExecContext(ctx, tasksNotifySessionsAt8); err != nil {
		return fmt.Errorf("recreating tasks_notify_sessions: %w", err)
	}
	return nil
}
