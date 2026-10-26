// E-2444: post-land plan items and their verdicts.
//
//   - task_plan_items.post_land / approved_post_land — the mark that an item's
//     work can only be done after the land, and the user's approval of it.
//   - task_post_land_entries — one row per post-land item per post-land
//     reconcile, keyed to the reconciliation in force at the land.
//
// The rationale lives beside them in internal/schema/schema.sql, and the DDL
// below must stay byte-comparable with it (TestMigrate_MatchesSchemaSQL).
//
// A .go step for 00021's reason: schema.sql already declares the columns, and
// SQLite has no ADD COLUMN IF NOT EXISTS, so each is probed. The table uses IF
// NOT EXISTS for the same reason.
//
// No Down: goose never rolls this set back in production.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func postLandItems() *goose.Migration {
	return goose.NewGoMigration(27, &goose.GoFunc{RunTx: postLandItemsUp}, nil)
}

var postLandColumns = []struct{ name, ddl string }{
	{"post_land", `ALTER TABLE task_plan_items ADD COLUMN post_land INTEGER NOT NULL DEFAULT 0`},
	{"approved_post_land", `ALTER TABLE task_plan_items ADD COLUMN approved_post_land INTEGER`},
}

const postLandEntriesTable = `CREATE TABLE IF NOT EXISTS task_post_land_entries (
    id INTEGER PRIMARY KEY,
    reconciliation_id INTEGER NOT NULL,
    attempt INTEGER NOT NULL,
    item_seq INTEGER NOT NULL,
    verdict TEXT NOT NULL,
    note TEXT,
    question_id INTEGER,
    holds_reason TEXT,
    head TEXT,
    session_id INTEGER,
    created_at TEXT NOT NULL,
    UNIQUE (reconciliation_id, attempt, item_seq),
    FOREIGN KEY (reconciliation_id) REFERENCES task_reconciliations(id) ON DELETE CASCADE
)`

func postLandItemsUp(ctx context.Context, tx *sql.Tx) error {
	for _, c := range postLandColumns {
		present, err := columnExists(ctx, tx, "task_plan_items", c.name)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if _, err = tx.ExecContext(ctx, c.ddl); err != nil {
			return fmt.Errorf("adding task_plan_items.%s: %w", c.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, postLandEntriesTable); err != nil {
		return fmt.Errorf("creating task_post_land_entries: %w", err)
	}
	return nil
}
