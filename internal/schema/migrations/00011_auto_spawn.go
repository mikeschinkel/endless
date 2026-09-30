// E-1814: the three columns auto-spawn needs.
//
//   - task_types.auto_spawnable — whether a task of this type may be spawned
//     without anyone asking. A property of the TYPE, mirrored from
//     tasktype.TaskType.AutoSpawnable() by seeds.sql and checked by
//     tasktype.VerifyIntegrity, so the selector filters on a column and never
//     lists a type in its own code.
//   - sessions.auto_spawned — set by SessionStart on a session whose window the
//     auto-spawn job opened. spawned_by stays NULL for these (E-1815): nothing
//     spawned them that a person could navigate to. The cap counts tasks whose
//     claiming session carries this flag.
//   - jobs.last_note — why the last run did what it did ("nothing eligible",
//     "no tmux client attached", "spawned E-N"). A skip is not a failure, so
//     last_error cannot carry it; `jobs list` shows it.
//
// E-2189's branch also holds a 00011. Whichever of the two lands second
// renumbers: goose refuses a duplicate version at provider construction, so the
// collision fails the first test run after the rebase rather than silently.
//
// A .go step for the reason the package header gives: schema.sql is exec'd
// directly and already declares all three, and SQLite has no ADD COLUMN IF NOT
// EXISTS. Each column is probed and skipped only when present.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func addAutoSpawn() *goose.Migration {
	return goose.NewGoMigration(11, &goose.GoFunc{RunTx: addAutoSpawnUp}, nil)
}

// autoSpawnColumns are added in this order. SQLite splices each after its
// table's last column, and schema.sql declares them in that same position.
var autoSpawnColumns = []struct{ table, column, ddl string }{
	{"task_types", "auto_spawnable", "auto_spawnable INTEGER NOT NULL DEFAULT 0"},
	{"sessions", "auto_spawned", "auto_spawned INTEGER NOT NULL DEFAULT 0"},
	{"jobs", "last_note", "last_note TEXT"},
}

func addAutoSpawnUp(ctx context.Context, tx *sql.Tx) error {
	for _, c := range autoSpawnColumns {
		present, err := columnExists(ctx, tx, c.table, c.column)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if _, err = tx.ExecContext(ctx,
			`ALTER TABLE `+c.table+` ADD COLUMN `+c.ddl,
		); err != nil {
			return fmt.Errorf("adding %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}
