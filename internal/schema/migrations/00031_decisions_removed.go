// E-2010: add decisions.removed and the live_decisions view, so removing a
// decision retains its row and its id can never be re-minted (ED-1547).
//
// `decisions` was the last entity with an EXPLICIT high-water allocator still
// paired with a hard delete: `SELECT COALESCE(MAX(id), 0) + 1 FROM decisions`
// in internal/events/executor.go. Deleting the highest decision handed its id
// to the next one minted, against every ED-NNNN already written into task
// descriptions, plans, commit subjects and CLAUDE.md files. The rationale lives
// beside the table and the view in internal/schema/schema.sql.
//
// A .go step for the reason the package header gives: schema.sql already
// declares the column, and SQLite has no ADD COLUMN IF NOT EXISTS, so it is
// probed and skipped only when present.
//
// The view is created with IF NOT EXISTS rather than dropped and recreated,
// because unlike 00025 this step introduces it — there is no earlier definition
// to replace, only schema.sql's identical one, which a directly-exec'd database
// may already hold. The statement must stay byte-comparable with schema.sql's:
// TestMigrate_MatchesSchemaSQL compares sqlite_master DDL between a migrated
// database and a schema.sql one, and SQLite stores a view's body verbatim.
//
// NO BACKFILL, and that is the honest answer rather than an omission. The rows
// whose ids were freed by the seven decision.deleted events in the ledger are
// gone, and nothing records what they held; retaining rows from here on is the
// entire remedy. One mercy: only the TOP of the range is ever re-freed, and all
// seven of those events name a single id that was not the highest, so no
// decision id has in fact been reused.
//
// No Down: goose never rolls this set back in production.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func decisionsRemoved() *goose.Migration {
	return goose.NewGoMigration(31, &goose.GoFunc{RunTx: decisionsRemovedUp}, nil)
}

func decisionsRemovedUp(ctx context.Context, tx *sql.Tx) error {
	present, err := columnExists(ctx, tx, "decisions", "removed")
	if err != nil {
		return err
	}
	if !present {
		if _, err = tx.ExecContext(ctx,
			`ALTER TABLE decisions ADD COLUMN removed INTEGER NOT NULL DEFAULT 0`,
		); err != nil {
			return fmt.Errorf("adding decisions.removed: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx,
		`CREATE VIEW IF NOT EXISTS live_decisions AS
    SELECT * FROM decisions WHERE removed = 0`,
	); err != nil {
		return fmt.Errorf("creating live_decisions: %w", err)
	}
	return nil
}
