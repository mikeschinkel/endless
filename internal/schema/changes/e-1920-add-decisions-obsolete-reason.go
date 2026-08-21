//go:build ignore

// E-1920: add decisions.obsolete_reason, backing the `obsolete` end state.
//
// Additive, so schema.sql declares the post-migration shape and this file
// brings existing DBs up to it. CREATE TABLE IF NOT EXISTS no-ops on a
// populated DB, so a new column never reaches one without a change file.
//
// A `.go` change rather than `.sql` because SQLite has no ADD COLUMN IF NOT
// EXISTS: the probe below makes this a no-op on a DB already built from
// schema.sql (fresh DBs, the sandbox, and tests all have the column from the
// CREATE TABLE), matching e-1917 and e-1929's precedent for the same
// operation. A plain `ALTER TABLE ... ADD COLUMN` in a .sql file would
// hard-error there instead. The _schema_version marker gates re-runs on top.
//
// The two new statuses themselves need no migration: `decisions.status` is
// TEXT with no CHECK constraint (schema.sql forbids them), and validation
// lives in application code — internal/events/decision.go's
// validDecisionStatuses on the Go side, the status guards in
// src/endless/decision_cmd.py on the Python side. Nothing to alter, which is
// why this file only carries the column.
//
// No index on decisions(status). The table is small enough that every read
// against it is already a scan, and per e-1929's note a CREATE INDEX in
// schema.sql resolves its columns eagerly — an index on a change-file column
// would abort schema application on every populated DB before this file could
// run.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` names the file explicitly and so
// runs it regardless.
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
			`SELECT count(*) FROM pragma_table_info('decisions')
			  WHERE name = 'obsolete_reason'`,
		).Scan(&present)
		if err != nil {
			return fmt.Errorf("probing decisions columns: %w", err)
		}
		if present == 0 {
			if _, err = tx.Exec(
				"ALTER TABLE decisions ADD COLUMN obsolete_reason TEXT",
			); err != nil {
				return fmt.Errorf("adding decisions.obsolete_reason: %w", err)
			}
		}
		return nil
	})
}
