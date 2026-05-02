package monitor

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mikeschinkel/go-doterr"
)

// CurrentSchemaVersion is the highest schema version this binary knows how
// to apply. Bump this when appending to the migrations slice below. The
// orchestrator uses CurrentSchemaVersion to decide what work is pending and
// to fast-path post-V4 databases that pre-date the framework.
const CurrentSchemaVersion = 4

// Migration describes one step in the schema evolution.
//
// Authoring contract:
//   - Append to the migrations slice in order; never reorder or renumber.
//   - Bump CurrentSchemaVersion to match the highest Version in the slice.
//   - Apply must be safe to run against a database that already has the
//     target schema (use IF NOT EXISTS, hasColumn, hasTable). The framework
//     gates re-runs by version, but a partial-failure rerun must still
//     converge instead of corrupting data.
//   - Set RequiresRebuild=true if Apply rebuilds a whole table (drop + copy).
//     Such migrations only run when the operator passes --force-rebuild;
//     auto-migrate (called by monitor.DB()) refuses to run them.
//   - No assumptions about prior runs: each Apply gets a fresh database
//     handle; no shared state. Use the framework's version gate, not your
//     own.
type Migration struct {
	Version         int
	Name            string
	Apply           func(*sql.DB) error
	RequiresRebuild bool
}

// migrations is the ordered registry of schema migrations. Append-only; see
// the Migration docstring for the authoring contract.
//
// V1-V4 already shipped before this framework existed. Their bodies are
// idempotent (IF NOT EXISTS / hasColumn / hasTable). The framework's
// version gate just stops them from re-running unnecessarily on existing
// installs.
var migrations = []Migration{
	{Version: 1, Name: "legacy plan->task + base tables", Apply: migrateV1},
	{Version: 2, Name: "drop dead tables, rename, tier", Apply: migrateV2},
	{Version: 3, Name: "session conversation history", Apply: migrateV3},
	{Version: 4, Name: "task_files, suggestions", Apply: migrateV4},
}

// RunnerLabel identifies who triggered a migration. Recorded in the
// _schema_version audit row.
type RunnerLabel string

const (
	RunnerAuto         RunnerLabel = "auto"
	RunnerExplicit     RunnerLabel = "explicit"
	RunnerForceRebuild RunnerLabel = "force-rebuild"
	RunnerBackfill     RunnerLabel = "backfill"
)

// MigrateOpts controls migration behavior.
type MigrateOpts struct {
	// Runner labels the audit row inserted into _schema_version. Defaults
	// to RunnerAuto when zero-valued.
	Runner RunnerLabel
	// AllowRebuild, when true, permits migrations with RequiresRebuild=true
	// to run. Defaults to false; auto-migrate must opt in to rebuilds via
	// the explicit `endless db migrate --force-rebuild` path.
	AllowRebuild bool
	// SkipBackup suppresses the pre-migration BackupDB call. Default false.
	SkipBackup bool
	// DryRun reports what would be applied without touching the database.
	DryRun bool
	// Target caps the highest version to apply. Zero means CurrentSchemaVersion.
	Target int
}

// MigrationStep is one entry in a MigrateResult.
type MigrationStep struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Reason  string `json:"reason,omitempty"`
}

// MigrateResult reports what migrate() did. Suitable for JSON output.
type MigrateResult struct {
	Applied []MigrationStep `json:"applied"`
	Skipped []MigrationStep `json:"skipped"`
}

// Sentinel errors for the migration framework.
var (
	ErrMigrationFailed   = errors.New("migration failed")
	ErrRequiresRebuild   = errors.New("migration requires --force-rebuild")
	ErrUnknownVersion    = errors.New("unknown schema version")
	ErrSchemaVersionRead = errors.New("read schema version failed")
)

// userVersion reads PRAGMA user_version.
func userVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow("PRAGMA user_version").Scan(&v)
	if err != nil {
		err = doterr.NewErr(ErrSchemaVersionRead, err)
		goto end
	}
end:
	return v, err
}

// setUserVersion writes PRAGMA user_version. SQLite does not bind PRAGMA
// arguments, so the integer is formatted directly. The caller controls the
// value, so format-string injection is not a concern.
func setUserVersion(db *sql.DB, v int) error {
	_, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v))
	return err
}

// ensureSchemaVersionTable creates the _schema_version audit table if it is
// missing. Idempotent.
func ensureSchemaVersionTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS _schema_version (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
		runner     TEXT NOT NULL DEFAULT 'auto'
	)`)
	return err
}

// migrationName returns the registered name for a version, or a synthetic
// name for versions older than this binary's registry (set by the legacy
// Python migrator before the framework existed).
func migrationName(v int) string {
	for _, m := range migrations {
		if m.Version == v {
			return m.Name
		}
	}
	return fmt.Sprintf("pre-framework migration v%d", v)
}

// backfillSchemaVersion inserts audit rows for versions 1..through that are
// not already recorded. Used to seed _schema_version on databases whose
// pragma was set before the framework existed.
func backfillSchemaVersion(db *sql.DB, through int) error {
	for v := 1; v <= through; v++ {
		_, err := db.Exec(
			"INSERT OR IGNORE INTO _schema_version (version, name, runner) VALUES (?, ?, ?)",
			v, migrationName(v), string(RunnerBackfill),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// migrate brings the database schema up to CurrentSchemaVersion (or
// opts.Target). Behavior:
//
//   - Always ensures the _schema_version table exists.
//   - If pragma user_version >= CurrentSchemaVersion, no migrations run;
//     missing audit rows are backfilled.
//   - If pragma user_version == 0 but the schema already looks post-V4
//     (heuristic: the suggestions table exists), fast-paths to
//     CurrentSchemaVersion without re-running V1-V4 bodies. Backfills the
//     audit rows. This skips the redundant backup that would otherwise be
//     taken on every existing install on first run after upgrade.
//   - Otherwise, runs each Migration with Version > current up to Target.
//     Each step bumps user_version atomically with its audit row insert.
//   - Migrations with RequiresRebuild=true block unless opts.AllowRebuild
//     is set; the operator must invoke --force-rebuild explicitly.
//
// The pragma is the gate; the table is the audit history. They stay in
// sync; on conflict, the higher of the two wins.
func migrate(db *sql.DB, opts MigrateOpts) (result MigrateResult, err error) {
	var (
		target int
		cur    int
	)
	if opts.Runner == "" {
		opts.Runner = RunnerAuto
	}
	target = CurrentSchemaVersion
	if opts.Target > 0 && opts.Target < target {
		target = opts.Target
	}

	err = ensureSchemaVersionTable(db)
	if err != nil {
		goto end
	}

	cur, err = userVersion(db)
	if err != nil {
		goto end
	}

	// Post-framework DB or DB patched by the E-1118 V5 stopgap: pragma is
	// already at or past CurrentSchemaVersion. Backfill the audit rows for
	// any prior versions that were applied before this framework existed
	// and return.
	if cur >= CurrentSchemaVersion {
		err = backfillSchemaVersion(db, cur)
		if err != nil {
			goto end
		}
		for _, m := range migrations {
			result.Skipped = append(result.Skipped, MigrationStep{
				Version: m.Version,
				Name:    m.Name,
				Reason:  "already applied",
			})
		}
		goto end
	}

	// Pre-framework existing install: pragma=0 but the schema is already
	// at V4. Don't re-run V1-V4 bodies (they're idempotent but the rerun
	// triggers an unwanted backup on every existing user). Fast-path.
	if cur == 0 && hasTable(db, "suggestions") {
		err = setUserVersion(db, CurrentSchemaVersion)
		if err != nil {
			goto end
		}
		err = backfillSchemaVersion(db, CurrentSchemaVersion)
		if err != nil {
			goto end
		}
		for _, m := range migrations {
			result.Skipped = append(result.Skipped, MigrationStep{
				Version: m.Version,
				Name:    m.Name,
				Reason:  "post-V4 install, backfilled",
			})
		}
		goto end
	}

	// Standard path: run each pending migration in order.
	if !opts.SkipBackup && !opts.DryRun {
		BackupDB()
	}
	for _, m := range migrations {
		if m.Version <= cur {
			result.Skipped = append(result.Skipped, MigrationStep{
				Version: m.Version,
				Name:    m.Name,
				Reason:  "already applied",
			})
			continue
		}
		if m.Version > target {
			result.Skipped = append(result.Skipped, MigrationStep{
				Version: m.Version,
				Name:    m.Name,
				Reason:  "above target",
			})
			continue
		}
		if m.RequiresRebuild && !opts.AllowRebuild {
			err = doterr.NewErr(
				ErrRequiresRebuild,
				doterr.IntKV("version", m.Version),
				doterr.StringKV("name", m.Name),
			)
			goto end
		}
		if opts.DryRun {
			result.Applied = append(result.Applied, MigrationStep{
				Version: m.Version,
				Name:    m.Name,
				Reason:  "dry-run",
			})
			continue
		}
		err = m.Apply(db)
		if err != nil {
			err = doterr.NewErr(
				ErrMigrationFailed,
				doterr.IntKV("version", m.Version),
				doterr.StringKV("name", m.Name),
				err,
			)
			goto end
		}
		err = setUserVersion(db, m.Version)
		if err != nil {
			goto end
		}
		_, err = db.Exec(
			"INSERT INTO _schema_version (version, name, runner) VALUES (?, ?, ?)",
			m.Version, m.Name, string(opts.Runner),
		)
		if err != nil {
			goto end
		}
		result.Applied = append(result.Applied, MigrationStep{
			Version: m.Version,
			Name:    m.Name,
		})
	}
end:
	return result, err
}

// Migrate is the public entry point for explicit migrate-db callers
// (cmd/endless-event migrate-db). monitor.DB() runs the auto path
// internally and does not go through this function.
func Migrate(opts MigrateOpts) (MigrateResult, error) {
	db, err := DB()
	if err != nil {
		return MigrateResult{}, err
	}
	return migrate(db, opts)
}
