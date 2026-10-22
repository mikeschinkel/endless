// E-2349: add projects.forgotten_from and keep forgotten rows out of
// live_projects.
//
// `endless project forget` tombstones a mistaken registration instead of
// deleting it: status 'forgotten', name and path rewritten to values nothing
// can collide with, identity and slug cleared. forgotten_from keeps what the
// row held before, as JSON, for debugging and posterity. The rationale lives
// beside the table in internal/schema/schema.sql.
//
// A .go step for the reason the package header gives: schema.sql already
// declares the column, and SQLite has no ADD COLUMN IF NOT EXISTS, so it is
// probed and skipped only when present. The view is dropped and recreated
// because SQLite cannot alter one.
//
// The view's `SELECT *` carries projects.path but nothing here touches the
// filesystem with it; the view's readers resolve it through
// monitor.ResolvedProjectPath as every reader of the column must (E-2011).
//
// No Down: goose never rolls this set back in production.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func forgottenProjects() *goose.Migration {
	return goose.NewGoMigration(25, &goose.GoFunc{RunTx: forgottenProjectsUp}, nil)
}

func forgottenProjectsUp(ctx context.Context, tx *sql.Tx) error {
	present, err := columnExists(ctx, tx, "projects", "forgotten_from")
	if err != nil {
		return err
	}
	if !present {
		if _, err = tx.ExecContext(ctx,
			`ALTER TABLE projects ADD COLUMN forgotten_from TEXT`,
		); err != nil {
			return fmt.Errorf("adding projects.forgotten_from: %w", err)
		}
	}
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS live_projects`,
		`CREATE VIEW live_projects AS
    SELECT * FROM projects WHERE status NOT IN ('ignored', 'forgotten')`,
	} {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("redefining live_projects: %w", err)
		}
	}
	return nil
}
