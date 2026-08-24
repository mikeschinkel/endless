//go:build ignore

// E-1969: rename sessions.active_task_id -> sessions.task_id,
// sessions.active_epic_id -> sessions.epic_id, and
// session_statuses.active_task_id -> session_statuses.task_id; then install the
// ED-1560 write-once trigger on sessions.task_id.
//
// `active_` named a distinction that no longer exists. It dates from when a
// session might hold several tasks with one of them active; a session now holds
// at most one task for its lifetime, so the qualifier describes nothing. Both
// sessions columns rename, not just the task one — renaming one and leaving the
// other trades a legacy name for a pair that disagrees with itself. And
// session_statuses carries its own copy of the same column with the same
// meaning, so it renames in step for the same reason.
//
// A `.go` change rather than `.sql` for two reasons, both about idempotency:
// SQLite has no `ALTER TABLE ... RENAME COLUMN IF EXISTS`, and a fresh DB built
// from schema.sql already has every post-rename name. Each rename below is
// therefore probed first, so this file is a no-op on a DB that never had the old
// names (fresh installs, the sandbox, every test DB) rather than a hard error.
// The _schema_version marker gates re-runs on top of that.
//
// ORDERING, and the reason the trigger is dropped and re-created around the
// renames. `endless db apply-change` opens the DB through monitor.DB() — which
// applies schema.sql — BEFORE it dispatches to this script. So the NEW
// schema.sql meets the OLD, pre-rename DB first. That is survivable for the
// CREATE TABLE (IF NOT EXISTS no-ops on the existing table) but NOT for the new
// trigger: SQLite resolves a trigger body lazily, so
// `CREATE TRIGGER ... BEFORE UPDATE OF task_id ON sessions` SUCCEEDS against a
// table whose column is still active_task_id — and then poisons the very rename
// this file exists to perform, because ALTER TABLE re-parses every trigger
// attached to the table:
//
//	SQL logic error: error in trigger sessions_task_id_write_once:
//	no such column: OLD.task_id (1)
//
// Dropping it first, renaming, and re-creating it is the whole fix. The
// re-create is not redundant with schema.sql: on a pre-rename DB the drop above
// removes what schema.sql just created, and nothing would put it back until the
// next connection.
//
// RENAME COLUMN needs SQLite >= 3.25 (2018). The project uses modernc.org/sqlite,
// which vendors a far newer engine (3.51 at time of writing) and is the only
// driver any endless binary opens the database with, so the floor is met by
// construction.
//
// SQLite rewrites the FOREIGN KEY clauses that name the renamed columns for
// free, so the post-rename table DDL matches schema.sql exactly. No index
// references either column.
//
// The trigger only constrains FUTURE writes. Rows already reassigned by the
// pre-write-once code stay exactly as they are — there is no data migration
// here, deliberately: the history of a bad binding is evidence, and erasing it
// is the class of destructive write E-1898 removed from this table.
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

// writeOnceTrigger is the ED-1560 enforcement, byte-for-byte what
// internal/schema/schema.sql declares for a fresh database. The two must not
// drift (ED-1472), so if you edit one, edit the other.
const writeOnceTrigger = `CREATE TRIGGER IF NOT EXISTS sessions_task_id_write_once
BEFORE UPDATE OF task_id ON sessions
WHEN OLD.task_id IS NOT NULL AND NEW.task_id IS NOT OLD.task_id
BEGIN
    SELECT RAISE(ABORT, 'sessions.task_id is write-once');
END`

// renames is every column this change moves, in the order it moves them.
var renames = []struct {
	table string
	from  string
	to    string
}{
	{"sessions", "active_task_id", "task_id"},
	{"sessions", "active_epic_id", "epic_id"},
	{"session_statuses", "active_task_id", "task_id"},
}

func main() {
	runner.Run(func(tx *sql.Tx) error {
		// Must precede every rename: see the ORDERING note at the top of the file.
		// IF EXISTS because on a fresh DB there is nothing to drop and on a
		// pre-rename DB schema.sql created a dangling one moments ago.
		if _, err := tx.Exec(
			"DROP TRIGGER IF EXISTS sessions_task_id_write_once",
		); err != nil {
			return fmt.Errorf("dropping write-once trigger before rename: %w", err)
		}

		for _, r := range renames {
			present, err := columnExists(tx, r.table, r.from)
			if err != nil {
				return err
			}
			if !present {
				// Already renamed, or a fresh DB that was born with the new name.
				continue
			}
			if _, err = tx.Exec(fmt.Sprintf(
				"ALTER TABLE %s RENAME COLUMN %s TO %s", r.table, r.from, r.to,
			)); err != nil {
				return fmt.Errorf("renaming %s.%s to %s: %w", r.table, r.from, r.to, err)
			}
			log.Printf("e-1969: renamed %s.%s -> %s", r.table, r.from, r.to)
		}

		if _, err := tx.Exec(writeOnceTrigger); err != nil {
			return fmt.Errorf("creating write-once trigger: %w", err)
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
