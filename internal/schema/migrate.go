package schema

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// migrationsFS holds the versioned migration set. Embedding it is what lets the
// set ship inside the binary: no installed copy of internal/schema/migrations/
// has to exist on the machine running endless, which matters because the
// binaries are symlinked into /usr/local/bin from a checkout the user is free
// to move.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// seedsSQL reconciles the enum mirror tables from the Go enums. Applied after
// every migration run rather than once — see seeds.sql for why.
//
//go:embed seeds.sql
var seedsSQL string

// BaselineVersion is the version 00001_baseline.sql carries: the shape
// internal/schema/schema.sql produced when E-2019 converted this project to
// goose.
//
// Nothing stamps a database at it. The baseline is written idempotently
// precisely so that the database that predates versioning -- there is exactly
// one, at ~/.config/endless/endless.db -- reaches this version by a replay that
// changes nothing, rather than by an assertion that it already had. A stamp
// would have to decide, from outside, that a database it cannot inspect deeply
// enough is 'already at the baseline'; the replay simply makes the claim true.
const BaselineVersion int64 = 1

// Migrate brings db up to the latest schema version and reconciles the enum
// mirrors. It is the one way a database in this project acquires its schema:
// monitor.DB(), the worktree sandbox seeder, the ledger projector and the tests
// all come through here, so none of them can build a database the others would
// not recognise.
//
// It is safe to call on every connect, which is what monitor.DB() does — E-2019
// changed where the schema comes from, not when it is applied. Against a
// database already at the latest version the whole call is two reads and the
// seed upserts.
func Migrate(db *sql.DB) error {
	return MigrateContext(context.Background(), db)
}

// MigrateContext is Migrate with a caller-supplied context.
func MigrateContext(ctx context.Context, db *sql.DB) error {
	provider, err := newProvider(db)
	if err != nil {
		return err
	}
	if _, err = provider.Up(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return Seed(db)
}

// Seed applies the enum mirror upserts. Migrate calls it; it is exported for
// the one caller that has already migrated and only wants the mirrors current.
func Seed(db *sql.DB) error {
	if _, err := db.Exec(seedsSQL); err != nil {
		return fmt.Errorf("seeding enum mirrors: %w", err)
	}
	return nil
}

// DBVersion reports the migration version db is stamped at. A database that has
// never met goose reports 0.
func DBVersion(ctx context.Context, db *sql.DB) (int64, error) {
	provider, err := newProvider(db)
	if err != nil {
		return 0, err
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	return version, nil
}

// LatestVersion reports the highest version in the embedded migration set — the
// version a database is brought to by Migrate. It reads the set, not a database,
// so it answers for a binary rather than for a connection: E-2020's direction
// rules are a comparison between this and DBVersion.
func LatestVersion() (int64, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return 0, fmt.Errorf("reading embedded migrations: %w", err)
	}
	var latest int64
	for _, entry := range entries {
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			return 0, fmt.Errorf("migration %q has no version prefix: %w", entry.Name(), err)
		}
		if version > latest {
			latest = version
		}
	}
	return latest, nil
}

// newProvider builds a goose provider over the embedded set.
//
// The provider API is used rather than goose's package-level functions because
// those keep the dialect, the base filesystem and the logger in package globals.
// A process that opens two databases — the sandbox seeder opens the main one to
// copy a project row — would be configuring one shared goose for both.
//
// WithLogger(NopLogger) is not cosmetic. goose's default logger writes "OK
// 00001_baseline.sql" to stdout, and endless-go subcommands write JSON there
// that the Python CLI parses. A chatty migration would make every first connect
// after a schema change look like a malformed event payload.
func newProvider(db *sql.DB) (*goose.Provider, error) {
	dir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("opening embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, dir,
		goose.WithLogger(goose.NopLogger()),
		// Nothing registers Go migrations globally, and inheriting whatever a
		// linked-in package might have registered is not a thing this project
		// wants to be exposed to.
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("configuring migrations: %w", err)
	}
	return provider, nil
}
