package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// faultRaisers is migration 16 (E-2268): which task and session raised a fault.
//
//   - errors.task_id, errors.session_id — the LATEST occurrence's raiser,
//     rewritten on every repeat, because the session to route an incident to is
//     the one that raised it most recently.
//   - errors_sources — one row per DISTINCT raiser of an incident, with its own
//     count and window, so the table grows with how broadly a fault was raised
//     rather than with how often.
//
// The rationale lives beside both in internal/schema/schema.sql. The DDL below
// must stay identical modulo whitespace to the declarations there
// (TestMigrate_MatchesSchemaSQL).
//
// A .go step for 00011's reason: schema.sql already declares the columns, and
// SQLite has no ADD COLUMN IF NOT EXISTS, so each is probed.
//
// No Down: the columns are NULL on every incident raised before this step, which
// is the same answer an unattributed fault gets going forward.
func faultRaisers() *goose.Migration {
	return goose.NewGoMigration(16, &goose.GoFunc{RunTx: faultRaisersUp}, nil)
}

// faultRaiserColumns are added in this order. SQLite splices each after the
// table's last column, and schema.sql declares them in that same position.
var faultRaiserColumns = []struct{ column, ddl string }{
	{"task_id", "task_id INTEGER"},
	{"session_id", "session_id INTEGER"},
}

// errorsSourcesAt16 is the table as of this migration. Frozen: a later change
// to it is a later migration, not an edit here.
const errorsSourcesAt16 = `CREATE TABLE IF NOT EXISTS errors_sources (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    error_id      INTEGER NOT NULL REFERENCES errors(id) ON DELETE CASCADE,
    session_id    INTEGER,
    task_id       INTEGER,
    occurrences   INTEGER NOT NULL DEFAULT 1,
    first_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_seen_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
)`

const errorsSourcesIndexAt16 = `CREATE UNIQUE INDEX IF NOT EXISTS idx_errors_sources_uniq
    ON errors_sources(error_id, COALESCE(session_id, 0), COALESCE(task_id, 0))`

func faultRaisersUp(ctx context.Context, tx *sql.Tx) error {
	for _, c := range faultRaiserColumns {
		present, err := columnExists(ctx, tx, "errors", c.column)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if _, err = tx.ExecContext(ctx, `ALTER TABLE errors ADD COLUMN `+c.ddl); err != nil {
			return fmt.Errorf("adding errors.%s: %w", c.column, err)
		}
	}
	if _, err := tx.ExecContext(ctx, errorsSourcesAt16); err != nil {
		return fmt.Errorf("creating errors_sources: %w", err)
	}
	if _, err := tx.ExecContext(ctx, errorsSourcesIndexAt16); err != nil {
		return fmt.Errorf("creating idx_errors_sources_uniq: %w", err)
	}
	return nil
}
