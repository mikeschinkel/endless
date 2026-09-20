package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// retireCuratedNextImportAndOrder is migration 3 (E-2142): retire the curated
// `next` list, `task import`, and session task ordering — five tables and two
// columns, in one step.
//
// Each object backed a surface whose reader was gone or going, and all three
// surfaces are removed in one land because they share one decision (the
// retired-kinds registry in internal/events/event.go):
//
//   - project_next{,_lanes,_tasks,_pending,_events} — the curated list
//     `endless task next revise` wrote. `endless project next`, the command that
//     would have displayed it, was never built, so this was a populated store
//     nothing read. `endless task next` is unrelated and stays.
//   - tasks.source_file — written only by `task import` / `task import-json`,
//     and read only to print a `Source:` line, to shape the export JSON, and to
//     enumerate a bulk clear. Endless was first conceived as a tool that
//     imported and synced markdown files; storing the markdown in the database
//     replaced that, and the product no longer thinks in source files. There is
//     no provenance worth preserving — do not reintroduce this column under
//     another name.
//   - session_tasks.do_order — the per-session implementation order
//     `endless session order` wrote, read only by `session status --tree`.
//     `--tree` STAYS and now derives its order from the blocked-by DAG alone,
//     which is what it already rendered for every session that never ran that
//     command.
//
// WHY THIS STEP IS `.go`
//
// SQLite has no DROP COLUMN IF EXISTS, and this step must tolerate a database
// that already lacks the columns — schema.sql no longer declares them and is
// still exec'd directly to build a database from the declared shape. The probes
// below are that tolerance, one named column at a time. See the package doc for
// why this is not the baseline's blanket idempotence.
//
// RunTx, so goose's transaction wraps the whole step: five drops and two alters
// either all land or none do. No PRAGMA is needed — nothing here is a table
// rebuild, which is the case migrate.go's enforceForeignKeys comment reserves
// `-- +goose NO TRANSACTION` for.
//
// DROP order is children-before-parents so the drops hold under
// PRAGMA foreign_keys=ON, which MigrateContext turns on before Up: the implicit
// row delete a DROP TABLE performs fires FK actions, and project_next_events is
// the one child whose FOREIGN KEY is not ON DELETE CASCADE. Indexes need no
// statement — SQLite drops a table's indexes with the table.
//
// ALTER TABLE ... DROP COLUMN rather than a table rebuild, because SQLite can do
// it for both columns: neither takes part in a constraint, an index, a trigger
// body or a view's column list. The two views over `tasks` (live_tasks,
// task_tree) select `*` / `t.*` and name no column, which is what keeps the
// whole-schema reparse DROP COLUMN performs from aborting on them. A rebuild
// would additionally have to re-create tasks_updated_at and
// tasks_notify_sessions, which DROP TABLE takes with it — easy to forget, and
// forgetting it silently stops the landed notice firing on every populated
// database.
//
// ORDERING, the same hazard e-2108 documents: once this applies, an endless-go
// binary older than it still names `source_file` in its task.created /
// task.imported INSERT and fails with "no such column". `just land` rebuilds the
// binaries immediately after main advances, so the window is inside one land and
// closes without intervention. Installing an unlanded build against the real
// database is what would widen it.
//
// No Down. Re-creating the tables would re-create five empty ones, and the
// columns' values are gone; a rollback that restores the shape without the data
// is a migration that lies about having reversed anything.
func retireCuratedNextImportAndOrder() *goose.Migration {
	return goose.NewGoMigration(3, &goose.GoFunc{RunTx: retireUp}, nil)
}

// retiredTables are the curated-next tables, children first.
var retiredTables = []string{
	"project_next_events",
	"project_next_pending",
	"project_next_tasks",
	"project_next_lanes",
	"project_next",
}

// retiredColumns are the columns to drop, as table → column.
var retiredColumns = []struct{ table, column string }{
	{"tasks", "source_file"},
	{"session_tasks", "do_order"},
}

func retireUp(ctx context.Context, tx *sql.Tx) error {
	for _, table := range retiredTables {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS "`+table+`"`); err != nil {
			return fmt.Errorf("dropping %s: %w", table, err)
		}
	}
	for _, c := range retiredColumns {
		present, err := columnExists(ctx, tx, c.table, c.column)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if _, err = tx.ExecContext(ctx,
			`ALTER TABLE "`+c.table+`" DROP COLUMN "`+c.column+`"`,
		); err != nil {
			return fmt.Errorf("dropping %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// columnExists reports whether table declares column.
//
// pragma_table_info as a table-valued function rather than `PRAGMA table_info`,
// so it can be a parameterized query inside the migration's transaction. The
// table name is still interpolated above because SQLite takes no parameter for
// an identifier; both names here are compile-time constants, not input.
func columnExists(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`,
		table, column,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("probing %s for %s: %w", table, column, err)
	}
	return n > 0, nil
}
