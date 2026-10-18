// E-2217: add projects.identity and projects.slug.
//
// One committed value, `name`, was doing three jobs: the project's identity,
// the machine-local key, and the stamp on every ledger event. These two columns
// take the first two jobs; the rationale for each lives beside the table in
// internal/schema/schema.sql.
//
// Existing projects get slug = name, the default every writer applies; an
// ignored directory's row (E-2251) is not a project and gets none. identity is
// left NULL: deriving it means running git in each project's checkout and
// writing its `.endless/config.json`, which is `endless project
// backfill-identity`'s job, not a schema step's.
//
// A .go step for the reason the package header gives: schema.sql already
// declares both columns, and SQLite has no ADD COLUMN IF NOT EXISTS, so each is
// probed and skipped only when present. The UNIQUE constraints are indexes
// because ALTER TABLE cannot add a UNIQUE column.
//
// No Down: goose never rolls this set back in production.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func addProjectIdentity() *goose.Migration {
	return goose.NewGoMigration(22, &goose.GoFunc{RunTx: addProjectIdentityUp}, nil)
}

func addProjectIdentityUp(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []string{"identity", "slug"} {
		present, err := columnExists(ctx, tx, "projects", column)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if _, err = tx.ExecContext(ctx,
			fmt.Sprintf(`ALTER TABLE projects ADD COLUMN %s TEXT`, column),
		); err != nil {
			return fmt.Errorf("adding projects.%s: %w", column, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE projects SET slug = name WHERE slug IS NULL AND status != 'ignored'`,
	); err != nil {
		return fmt.Errorf("defaulting projects.slug to name: %w", err)
	}
	for _, stmt := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_identity ON projects(identity)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("indexing projects: %w", err)
		}
	}
	return nil
}
