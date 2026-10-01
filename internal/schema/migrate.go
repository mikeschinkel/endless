package schema

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"

	"github.com/mikeschinkel/endless/internal/schema/migrations"
)

// migrationsFS holds the SQL half of the versioned migration set. Embedding it
// is what lets the set ship inside the binary: no installed copy of
// internal/schema/migrations/ has to exist on the machine running endless, which
// matters because the binaries are symlinked into /usr/local/bin from a checkout
// the user is free to move.
//
// The Go half compiles in rather than embedding, and comes from
// migrations.Go(). Both halves live in the same directory — see that package's
// doc for when a step has to be Go — so anything that answers "what is the
// migration set" has to consult both. There are two such things, newProvider
// and LatestVersion, and they are the reason this comment exists.
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
// Since E-2020 the connect no longer calls it on every open: monitor.DB()
// compares versions and calls it only when the database is behind. Against a
// database already at the latest version the whole call is two reads and the
// seed upserts.
func Migrate(db *sql.DB) error {
	return MigrateContext(context.Background(), db)
}

// MigrateContext is Migrate with a caller-supplied context.
func MigrateContext(ctx context.Context, db *sql.DB) error {
	provider, err := newProvider(ctx, db)
	if err != nil {
		return err
	}
	if err = enforceForeignKeys(ctx, db); err != nil {
		return err
	}
	if _, err = provider.Up(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return Seed(db)
}

// MigrateToContext brings db up to exactly version — Migrate, stopped short. It
// exists to build a database that is BEHIND the binary on purpose, which is the
// condition E-2020's connect rules act on and so the one their tests have to be
// able to construct.
//
// It reconciles the enum mirrors only when version is the latest. seeds.sql is
// written against the latest schema, so a behind database may lack a column it
// names: E-1814's migration 11 added task_types.auto_spawnable, and seeding a
// version-10 database with it failed. A behind database is reseeded by the
// connect that migrates it forward, which is the path under test.
func MigrateToContext(ctx context.Context, db *sql.DB, version int64) error {
	provider, err := newProvider(ctx, db)
	if err != nil {
		return err
	}
	if err = enforceForeignKeys(ctx, db); err != nil {
		return err
	}
	if _, err = provider.UpTo(ctx, version); err != nil {
		return fmt.Errorf("applying migrations up to %d: %w", version, err)
	}
	latest, err := LatestVersion()
	if err != nil {
		return err
	}
	if version < latest {
		return nil
	}
	return Seed(db)
}

// enforceForeignKeys turns foreign key enforcement on for db's connection.
//
// This is the one piece of connection state Migrate owns, and it owns it
// because schema.sql used to. Its first three lines were PRAGMAs, so every
// `Exec(schema.SQL)` configured the connection as a side effect of applying the
// schema; dropping all three silently disarmed every FK declaration the schema
// makes, in production and in 34 test files, and the only visible symptom was
// one faults test noticing that a deliberately dangling project id was no
// longer rejected. Behaviour that is load-bearing somewhere and invisible
// everywhere else does not get to leave by accident.
//
// The other two do NOT come along. journal_mode and busy_timeout are durability
// and concurrency policy, chosen per opener and, in journal_mode's case,
// persisted in the file — a migration has no business deciding either. Foreign
// keys are different in kind: the schema DECLARES them, so enforcing them is
// part of what it means for a database to have this schema.
//
// Set before Up, not inside it: SQLite ignores this pragma within a
// transaction, and goose wraps a migration in one. A future migration that
// needs enforcement off for a table rebuild has to declare itself
// `-- +goose NO TRANSACTION` and turn it off for itself.
func enforceForeignKeys(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enabling foreign key enforcement: %w", err)
	}
	return nil
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
	provider, err := newProvider(ctx, db)
	if err != nil {
		return 0, err
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	return version, nil
}

// LatestVersion reports the highest version in the migration set — the version a
// database is brought to by Migrate. It reads the set, not a database, so it
// answers for a binary rather than for a connection: E-2020's direction rules
// are a comparison between this and DBVersion.
//
// It counts BOTH halves of the set. Counting only the embedded .sql files would
// under-report the moment a Go step is the newest one, and under-reporting here
// is not cosmetic: `endless-go event migrate` would print a database version
// above its own latest, which is exactly the "your database is ahead of your
// binary" condition E-2020 exists to detect. A wrong answer there reads as a
// real fault rather than as a bug in the counting.
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
	for _, m := range migrations.Go() {
		if m.Version > latest {
			latest = m.Version
		}
	}
	return latest, nil
}

// newProvider builds a goose provider over the embedded set, after
// refuseUnversioned has had its say.
//
// The refusal lives here rather than in MigrateContext because every door to
// goose — DBVersion, MigrateContext, MigrateToContext — comes through this one,
// and the first goose call on a database CREATES goose_db_version. A connect
// reads the version before it migrates, so a check that waited for Migrate
// would find the table goose had just made and wave the database through.
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
func newProvider(ctx context.Context, db *sql.DB) (*goose.Provider, error) {
	if err := refuseUnversioned(ctx, db); err != nil {
		return nil, err
	}
	dir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("opening embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, dir,
		goose.WithLogger(goose.NopLogger()),
		// The Go half of the set, named explicitly. Registration is a slice in
		// migrations.Go() rather than init() side effects precisely so this is
		// the only door — which is what makes disabling the global registry
		// below a statement about correctness and not just about hygiene.
		goose.WithGoMigrations(migrations.Go()...),
		// Nothing else may register Go migrations: inheriting whatever a
		// linked-in package might have put in goose's package-level registry is
		// not a thing this project wants to be exposed to.
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("configuring migrations: %w", err)
	}
	return provider, nil
}

// legacyLadderTop is the PRAGMA user_version the retired Python migration
// ladder left a database at once it had taken every step. A database below it
// that also predates goose never took the ladder to the end.
const legacyLadderTop = 6

// ErrDatabaseTooOld is the refusal for a database that predates both
// versioning schemes.
var ErrDatabaseTooOld = errors.New("database predates schema versioning and is too old to upgrade in place")

// refuseUnversioned refuses a database that predates both versioning schemes:
// it holds Endless data (a projects table), goose has never stamped it (no
// goose_db_version), and the retired Python ladder never finished with it
// (PRAGMA user_version below legacyLadderTop).
//
// Running goose on such a database would be worse than refusing. The baseline
// is idempotent CREATE ... IF NOT EXISTS, so it would add the missing TABLES,
// pass over every missing COLUMN of the tables that exist, and stamp the result
// version 1 — a database that looks current and is not, with its age erased.
// E-2158 deleted the ladder that once brought such a database forward, so the
// honest answer is to stop and say so. Nothing is written first: the checks are
// reads, and they run before goose has created its table.
//
// Every database Go builds is stamped by goose on its first migration, and the
// real ledger is too, so neither ever reaches the refusal. An empty file has
// no projects table and is simply built.
func refuseUnversioned(ctx context.Context, db *sql.DB) error {
	var hasProjects, hasGoose bool
	var userVersion int64

	err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='projects'),
		       EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='goose_db_version')`,
	).Scan(&hasProjects, &hasGoose)
	if err != nil {
		return fmt.Errorf("inspecting the database's versioning: %w", err)
	}
	if !hasProjects || hasGoose {
		return nil
	}
	err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion)
	if err != nil {
		return fmt.Errorf("reading PRAGMA user_version: %w", err)
	}
	if userVersion >= legacyLadderTop {
		return nil
	}
	return fmt.Errorf("%w: %s has no goose_db_version table and PRAGMA user_version %d "+
		"(below %d); its schema cannot be brought forward safely, so it was left untouched",
		ErrDatabaseTooOld, databaseFile(ctx, db), userVersion, legacyLadderTop)
}

// databaseFile names the file db is open on, for a message. An in-memory
// database, or one whose name cannot be read, is described rather than named.
func databaseFile(ctx context.Context, db *sql.DB) string {
	var seq int
	var name, file string

	err := db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &file)
	if err != nil || file == "" {
		return "the database"
	}
	return file
}
