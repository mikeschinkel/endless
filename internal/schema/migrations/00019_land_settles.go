package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// landSettles is migration 19 (E-2262): the schema half of a land that settles
// its task.
//
//   - task_types.lands, .requires_verify_suite, .settles_on_land — properties of
//     the TYPE, mirrored from tasktype by schema.sql's seed and checked by
//     tasktype.VerifyIntegrity, exactly as 00011 added auto_spawnable.
//   - tasks.verified_sha — the commit a user's passing verify ran at, carried by
//     the task.status_changed event into `unlanded`.
//
// A .go step for 00011's reason: schema.sql already declares the columns, and
// SQLite has no ADD COLUMN IF NOT EXISTS, so each is probed.
//
// No Down: a ledger rebuild recomputes verified_sha, and the seed rewrites the
// type columns on every connect.
func landSettles() *goose.Migration {
	return goose.NewGoMigration(19, &goose.GoFunc{RunTx: landSettlesUp}, nil)
}

// landSettlesColumns are added in this order. SQLite splices each after its
// table's last column, and schema.sql declares them in that same position.
var landSettlesColumns = []struct{ table, column, ddl string }{
	{"task_types", "lands", "lands INTEGER NOT NULL DEFAULT 1"},
	{"task_types", "requires_verify_suite", "requires_verify_suite INTEGER NOT NULL DEFAULT 0"},
	{"task_types", "settles_on_land", "settles_on_land INTEGER NOT NULL DEFAULT 0"},
	{"tasks", "verified_sha", "verified_sha TEXT"},
}

func landSettlesUp(ctx context.Context, tx *sql.Tx) error {
	for _, c := range landSettlesColumns {
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
