//go:build ignore

// E-1000: rename tasks.text -> tasks.plan.
//
// The field has been called "the plan" in every conversation and every guide
// for as long as it has existed; only the column, the flag and the event key
// still said `text`. This closes that gap. `decisions.text` is deliberately NOT
// renamed — E-1868 rewrites decision storage wholesale, so renaming it here is
// work that gets thrown away.
//
// A `.go` change rather than `.sql` for the same two idempotency reasons
// e-1969-rename-sessions-task-id.go gives: SQLite has no
// `ALTER TABLE ... RENAME COLUMN IF EXISTS`, and a fresh DB built from
// schema.sql is already born with `plan`. The rename below is therefore probed
// first, so this file is a no-op on a DB that never had the old name (fresh
// installs, the sandbox, every test DB) rather than a hard error. The
// _schema_version marker gates re-runs on top of that.
//
// ORDERING, and the reason tasks_notify_sessions is dropped and re-created
// around the rename. Two separate problems, both solved by the drop:
//
//  1. ALTER TABLE re-parses every trigger attached to the table. SQLite would
//     happily rewrite OLD.text/NEW.text to OLD.plan/NEW.plan for us — but see
//     (2) for why that is not enough, and dropping first removes the re-parse
//     as a failure mode entirely.
//
//  2. The trigger body carries the STRING LITERAL 'text' as the key it writes
//     into session_notices.changes. SQLite rewrites column references on
//     RENAME COLUMN; it does not rewrite string literals. A trigger left in
//     place would keep emitting `{"text": …}` notices against a column now
//     called plan — the schema and the notice vocabulary disagreeing silently,
//     which is the whole failure this task exists to end.
//
// `endless db apply-change` opens the DB through monitor.DB() — which applies
// schema.sql — BEFORE it dispatches here, so the NEW schema.sql meets the OLD,
// pre-rename DB first. `CREATE TRIGGER IF NOT EXISTS` no-ops against the
// already-present old trigger, so the pre-rename DB still holds the 'text'
// version at the moment this script starts. Dropping it, renaming, and
// re-creating from the constant below is what actually installs the new body.
// The re-create is not redundant with schema.sql: on a pre-rename DB the drop
// removes what schema.sql just no-op'd over, and nothing would put it back
// until the next connection.
//
// RENAME COLUMN needs SQLite >= 3.25 (2018). The project uses modernc.org/sqlite,
// which vendors a far newer engine and is the only driver any endless binary
// opens the database with, so the floor is met by construction.
//
// live_tasks is `SELECT * FROM tasks WHERE removed = 0` — it names no column, so
// the rename does not touch it and it serves `plan` from the next PREPARE on.
// No index references the column.
//
// There is no data migration: RENAME COLUMN moves the values with the column.
// Historical ledger events keyed `text` keep projecting into this column via the
// legacy map entries in internal/events/{projector,executor}.go.
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

// notifyTrigger is byte-for-byte what internal/schema/schema.sql declares for a
// fresh database. The two must not drift (ED-1472), so if you edit one, edit
// the other.
const notifyTrigger = `CREATE TRIGGER IF NOT EXISTS tasks_notify_sessions AFTER UPDATE ON tasks
WHEN OLD.status      IS NOT NEW.status
  OR OLD.phase       IS NOT NEW.phase
  OR OLD.tier        IS NOT NEW.tier
  OR OLD.description IS NOT NEW.description
  OR OLD.plan        IS NOT NEW.plan
  OR OLD.analysis    IS NOT NEW.analysis
  OR OLD.notes       IS NOT NEW.notes
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
                SELECT 'tier',
                       json_object('before', OLD.tier, 'after', NEW.tier)
                 WHERE OLD.tier IS NOT NEW.tier
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
                UNION ALL
                SELECT 'plan',
                       json_object(
                           'before', CASE WHEN OLD.plan IS NULL THEN NULL
                                          WHEN OLD.plan = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.plan IS NULL THEN NULL
                                          WHEN NEW.plan = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.plan IS NOT NEW.plan
                UNION ALL
                SELECT 'analysis',
                       json_object(
                           'before', CASE WHEN OLD.analysis IS NULL THEN NULL
                                          WHEN OLD.analysis = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.analysis IS NULL THEN NULL
                                          WHEN NEW.analysis = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.analysis IS NOT NEW.analysis
                UNION ALL
                SELECT 'notes',
                       json_object(
                           'before', CASE WHEN OLD.notes IS NULL THEN NULL
                                          WHEN OLD.notes = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.notes IS NULL THEN NULL
                                          WHEN NEW.notes = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.notes IS NOT NEW.notes
           )),
           strftime('%Y-%m-%dT%H:%M:%S', 'now'),
           NEW.changed_by_session
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
     WHERE st.task_id = NEW.id
       AND st.session_id IS NOT NEW.changed_by_session
       AND s.state != 'ended';
END;`

func main() {
	runner.Run(func(tx *sql.Tx) error {
		// Must precede the rename: see the ORDERING note at the top of the file.
		// IF EXISTS because on a fresh DB schema.sql created the NEW trigger
		// moments ago and on a pre-rename DB it left the old one in place —
		// either way the CREATE below installs the authoritative body.
		if _, err := tx.Exec(
			"DROP TRIGGER IF EXISTS tasks_notify_sessions",
		); err != nil {
			return fmt.Errorf("dropping tasks_notify_sessions before rename: %w", err)
		}

		present, err := columnExists(tx, "tasks", "text")
		if err != nil {
			return err
		}
		if present {
			if _, err = tx.Exec(
				"ALTER TABLE tasks RENAME COLUMN text TO plan",
			); err != nil {
				return fmt.Errorf("renaming tasks.text to plan: %w", err)
			}
			log.Printf("e-1000: renamed tasks.text -> tasks.plan")
		}

		if _, err := tx.Exec(notifyTrigger); err != nil {
			return fmt.Errorf("creating tasks_notify_sessions: %w", err)
		}

		return nil
	})
}

// columnExists reports whether the named column is present on the named table.
// A table that does not exist at all reports false, which is the right answer
// for "is there an old name here to rename".
func columnExists(tx *sql.Tx, table, column string) (bool, error) {
	var n int
	if err := tx.QueryRow(
		"SELECT count(*) FROM pragma_table_info(?) WHERE name = ?", table, column,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("probing %s columns: %w", table, err)
	}
	return n > 0, nil
}
