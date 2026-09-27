package schema

import (
	"context"
	"database/sql"
)

// MigrateUpTo brings db to exactly version, for a test that needs a database
// shaped as it was before a later step — the only way to exercise a step's
// data movement, since Migrate always runs the whole set.
func MigrateUpTo(db *sql.DB, version int64) error {
	provider, err := newProvider(db)
	if err != nil {
		return err
	}
	if err = enforceForeignKeys(context.Background(), db); err != nil {
		return err
	}
	_, err = provider.UpTo(context.Background(), version)
	return err
}
