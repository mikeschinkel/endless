//go:build ignore

// E-1960: add errors.project_id and re-qualify the open-incident unique index.
//
// One Endless database holds every project on the machine, but the `errors`
// table E-698 created had no project column. Every recorded fault from every
// project landed in one undifferentiated table: `errors show` could not filter,
// the status badge could not scope, and — worse than either — the partial
// unique index on (source, code, fingerprint) COLLIDED across unrelated
// projects, so two projects hitting the same condition became one incident with
// a doubled occurrence count and a summary from whichever wrote last.
//
// Additive, so schema.sql declares the post-migration shape and this file brings
// existing DBs up to it. CREATE TABLE IF NOT EXISTS no-ops on a populated DB, so
// a new column never reaches one without a change file.
//
// A `.go` change rather than `.sql` for e-1929's reason: SQLite has no
// ADD COLUMN IF NOT EXISTS, so the probe below makes this a no-op on a DB
// already built from schema.sql (fresh DBs, the sandbox, tests), where a plain
// ALTER in a .sql file would hard-error instead.
//
// # Backfill: NULL, deliberately
//
// Existing rows keep project_id NULL, and nothing is reconstructed. There is
// nothing to reconstruct FROM: a row carries `source` ("job:evaluate",
// "worktree:unsettled"), a fingerprint and a summary, none of which name a
// project. The one place attribution could be inferred is the JSONL detail log's
// Fields — a `worktree` path that could be walked back to a projects row — and
// that is archaeology over a best-effort, machine-local, torn-line-tolerant log
// to date rows that are already history. NULL is also not a lie here: it is the
// same value a fault with no resolvable project gets going forward (the job
// runner's own failures), and every project-scoped read includes NULL-project
// incidents for exactly that reason. The real table this was measured against
// held 9 rows, all of them already cleared.
//
// # Why the index is dropped and recreated rather than added beside
//
// The old index has to GO, not merely be joined by a better one: while it
// exists, two projects still cannot hold the same open fingerprint. Its NAME is
// reused because schema.sql runs on every connection before this file gets to
// run — a differently-named CREATE ... IF NOT EXISTS naming project_id would
// abort schema application on the very DBs that need migrating (the ordering
// trap e-1929 documents at length). Keeping the name lets schema.sql's
// IF NOT EXISTS short-circuit on the old index, and this file swaps the
// definition underneath it.
//
// COALESCE(project_id, 0) rather than the bare column because SQLite treats
// NULLs as distinct in a unique index: a bare project_id would silently stop
// deduplicating unattributed faults — the tmux status bar re-execs per pane
// every two seconds — and turn one open incident into thousands of rows.
//
// Ordering inside the transaction matters: the column must exist before an index
// over it can be created, and the old index must be dropped before the new one
// can take its name.
//
// A migrated table carries project_id LAST, where ADD COLUMN puts it, while a
// fresh one declares it second. That positional difference is inherent to
// ALTER TABLE and shared by every prior additive change here (e-1929's
// tasks.removed among them); it is harmless because nothing in this codebase
// reads a column by ordinal — every statement names its columns.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` (the apply-change dispatcher) still
// executes it.
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
			`SELECT count(*) FROM pragma_table_info('errors')
			  WHERE name = 'project_id'`,
		).Scan(&present)
		if err != nil {
			return fmt.Errorf("probing errors columns: %w", err)
		}
		if present == 0 {
			// No DEFAULT, so the implicit default is NULL — which is what
			// ALTER TABLE ADD COLUMN requires of a column carrying a REFERENCES
			// clause, and what the backfill decision above wants anyway.
			//
			// One line, unwrapped: SQLite stores the statement's own text inside
			// the rebuilt CREATE TABLE, so a wrapped literal would leave this
			// file's Go indentation embedded in the schema `endless sql` prints.
			if _, err = tx.Exec(
				`ALTER TABLE errors ADD COLUMN project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL`,
			); err != nil {
				return fmt.Errorf("adding errors.project_id: %w", err)
			}
		}

		// Unconditional, not gated on `present`: a DB whose column was added by
		// an earlier partial run still carries the OLD index definition, and
		// leaving it would keep two projects from holding the same open
		// fingerprint. Both statements are idempotent, so re-running is free.
		if _, err = tx.Exec(`DROP INDEX IF EXISTS idx_errors_open_uniq`); err != nil {
			return fmt.Errorf("dropping the old open-incident index: %w", err)
		}
		if _, err = tx.Exec(
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_errors_open_uniq
			     ON errors(COALESCE(project_id, 0), source, code, fingerprint)
			  WHERE cleared_at IS NULL`,
		); err != nil {
			return fmt.Errorf("creating the project-qualified open-incident index: %w", err)
		}

		return nil
	})
}
