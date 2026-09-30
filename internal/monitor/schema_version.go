package monitor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/schema"
)

// The connect-time schema rules (E-2020, ED-1570, ED-1601).
//
// A connect no longer brings the database up to date as a matter of course. It
// compares the version the database is recorded at against the newest version
// this binary carries and acts by DIRECTION. Before any of that, it asks who is
// connecting to what: a binary built inside a task worktree never opens the
// main database, at any version (ED-1601) — the installed binary talks to main,
// a worktree's binary talks to its sandbox.

// connectAction is what DB() does about the schema once it has a connection.
type connectAction int

const (
	// actionCurrent: the versions agree; nothing to apply.
	actionCurrent connectAction = iota
	// actionApply: the database is BEHIND. The binary needs DDL the database
	// lacks and owns the database it is talking to — the worktree-build/main
	// pairing was refused before a connection existed — so it applies forward,
	// after a backup.
	actionApply
	// actionHalt: the database is AHEAD. Per ED-1570 a binary works with exactly
	// one version and there is no compatibility window to evaluate: this binary
	// must be upgraded, and it writes nothing meanwhile.
	actionHalt
)

// connectActionFor is the direction rule, pure so it can be proven without a
// process, a home directory or a database.
func connectActionFor(dbVersion, latest int64) connectAction {
	switch {
	case dbVersion < latest:
		return actionApply
	case dbVersion > latest:
		return actionHalt
	}
	return actionCurrent
}

// refusesMainDB is the ED-1601 rule, pure for the same reason: a binary built
// inside a task worktree never opens the main database.
//
// It replaces E-1818's schema-passive mode. That mode let a worktree build open
// main and skipped the schema work and the enum gates for it; but the gates
// fail-close on EXTRA mirror rows, so a worktree build that reseeded main with
// a branch-only enum value would have fail-closed the installed binary
// machine-wide, and one that did not reseed wrote rows against mirrors it did
// not match. With worktree builds off main entirely, only the installed binary
// ever migrates, seeds or gates it, and DB() has no ownership branch left.
func refusesMainDB(worktreeBuild, mainDB bool) bool {
	return worktreeBuild && mainDB
}

// ErrSchemaRefused is matched (errors.Is) by every refusal DB() raises about
// the schema rather than about the file: a worktree build aimed at main, a
// database ahead of the binary, and a forward migration that did not complete.
//
// It exists so a surface can render all three the way its reader needs without
// parsing prose: the Claude hook, the tmux status line and the background jobs
// fire constantly for nobody, so they record one fault and stay silent, while
// an interactive command prints the refusal.
var ErrSchemaRefused = errors.New("database refused by this endless-go")

// SchemaRefusal is the concrete error. Summary is the stable one-line form a
// fault is fingerprinted on — it carries the versions and the kind but not the
// path or the underlying error, so every event that trips over the same
// mismatch lands on one incident.
type SchemaRefusal struct {
	Kind          string // "worktree-build-on-main", "database-ahead" or "upgrade-failed"
	DBPath        string
	DBVersion     int64
	BinaryVersion int64
	Binary        string
	Cause         error
	message       string
}

func (e *SchemaRefusal) Error() string { return e.message }

// Is makes every SchemaRefusal match ErrSchemaRefused.
func (e *SchemaRefusal) Is(target error) bool { return target == ErrSchemaRefused }

func (e *SchemaRefusal) Unwrap() error { return e.Cause }

// Summary is the fingerprintable one-liner.
func (e *SchemaRefusal) Summary() string {
	switch e.Kind {
	case "worktree-build-on-main":
		return "a worktree-built endless-go refused the main database"
	case "database-ahead":
		return fmt.Sprintf("database is at schema v%d, endless-go carries v%d: upgrade endless",
			e.DBVersion, e.BinaryVersion)
	}
	return fmt.Sprintf("migrating the database from schema v%d to v%d did not complete",
		e.DBVersion, e.BinaryVersion)
}

// worktreeBuildRefusal is raised before the database is opened.
func worktreeBuildRefusal(path string) *SchemaRefusal {
	exe := executablePath()
	return &SchemaRefusal{
		Kind:   "worktree-build-on-main",
		DBPath: path,
		Binary: exe,
		message: fmt.Sprintf(
			"refusing to open the main database: this endless-go was built in a "+
				"task worktree, and a worktree build never opens main (ED-1601).\n\n"+
				"  binary:   %s\n"+
				"  database: %s\n\n"+
				"Run the installed endless-go for the main database — `endless "+
				"--db main ...` already does — or pass --db sandbox to use this "+
				"worktree's own database.",
			exe, path),
	}
}

// executablePath is this process's binary, symlinks resolved, or "".
func executablePath() string {
	exe, err := osExecutable()
	if err != nil {
		return ""
	}
	return resolvedPath(exe)
}

// databaseAheadRefusal is the halt: the binary is older than the database.
func databaseAheadRefusal(path string, dbVersion, latest int64) *SchemaRefusal {
	return &SchemaRefusal{
		Kind:          "database-ahead",
		Binary:        executablePath(),
		DBPath:        path,
		DBVersion:     dbVersion,
		BinaryVersion: latest,
		message: fmt.Sprintf(
			"refusing to use %s: the database is at schema version %d and this "+
				"endless-go carries version %d, so the binary is older than the "+
				"database and may not write to it (ED-1570).\n\n"+
				"Upgrade endless to a release carrying version %d. To go back "+
				"instead, `endless db restore` returns the database to the backup "+
				"taken before it was upgraded.",
			path, dbVersion, latest, dbVersion),
	}
}

// upgradeFailedRefusal covers a forward migration that failed, or the backup
// that must precede it.
func upgradeFailedRefusal(path string, dbVersion, latest int64, cause error) *SchemaRefusal {
	return &SchemaRefusal{
		Kind:          "upgrade-failed",
		Binary:        executablePath(),
		DBPath:        path,
		DBVersion:     dbVersion,
		BinaryVersion: latest,
		Cause:         cause,
		message: fmt.Sprintf(
			"the database at %s is at schema version %d and this endless-go "+
				"needs version %d, but bringing it forward failed: %v\n\n"+
				"Run `endless db upgrade` to retry; it backs the database up first "+
				"and reports what it applied.",
			path, dbVersion, latest, cause),
	}
}

// reconcileSchema applies the direction rule to an open connection. On
// actionApply it backs the database up and migrates forward; it returns a
// SchemaRefusal for a halt or a failed upgrade.
//
// A database at version 0 has never met goose — a file sql.Open just created —
// so there is nothing in it to protect and no backup is taken. Every real
// database has been recorded at the baseline since E-2019.
func reconcileSchema(db *sql.DB, path string) error {
	ctx := context.Background()

	version, err := schema.DBVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("reading the schema version of %s: %w", path, err)
	}
	latest, err := schema.LatestVersion()
	if err != nil {
		return err
	}

	switch connectActionFor(version, latest) {
	case actionHalt:
		return databaseAheadRefusal(path, version, latest)
	case actionApply:
		if version > 0 {
			if _, err := BackupDBContext(ctx); err != nil {
				return upgradeFailedRefusal(path, version, latest,
					fmt.Errorf("backing up before migrating: %w", err))
			}
		}
		if err := schema.MigrateContext(ctx, db); err != nil {
			return upgradeFailedRefusal(path, version, latest, err)
		}
		return nil
	}

	// Current. The enum mirrors are DATA reconciled on every connect, not a
	// migration (seeds.sql says why), so they are seeded here too — before the
	// integrity gates read them, which is what keeps E-1659's self-heal.
	if err := schema.Seed(db); err != nil {
		return fmt.Errorf("seeding %s: %w", path, err)
	}
	return nil
}

// faultConn is a connection DB() opened and then refused on schema grounds. It
// is handed to the fault writer ALONE (FaultDB), so a hook meeting a database
// ahead of its binary — every hook on the machine, during a land's window —
// records one deduplicated incident instead of an unindexed line per event.
// The `errors` table is machine-local observation, not ledger (package faults),
// which is why an older binary writing it is not the ED-1570 hazard.
var faultConn *sql.DB

// FaultDB is the accessor internal/faults is bound to. It is DB() whenever DB()
// succeeds, and the refused connection when DB() opened one and then refused
// it. A worktree build aimed at main never opens it, so it gets no handle and
// its faults go to the unindexed log in its own config directory.
func FaultDB() (*sql.DB, error) {
	db, err := DB()
	if err == nil {
		return db, nil
	}
	if faultConn != nil {
		return faultConn, nil
	}
	return nil, err
}

// RecordSchemaRefusal files err as one ERR-0020 occurrence when it is a schema
// refusal, and reports whether it was. The silent surfaces — the Claude hook,
// the tmux status line, the background jobs — call it and, on true, render
// nothing: they fire on every event for nobody, so the refusal belongs in the
// fault record and not on a stream nobody reads.
//
// Source is "connect" whatever the surface, because the incident is about the
// connect, not about who happened to trip over it: one mismatch is one incident
// machine-wide, with the surface recorded in the detail.
func RecordSchemaRefusal(surface string, err error) bool {
	var refusal *SchemaRefusal
	if !errors.As(err, &refusal) {
		return false
	}
	faults.Record(faults.Fault{
		Code:        faults.ErrCodeSchemaVersionRefused,
		Source:      "connect",
		Fingerprint: refusal.Kind + ":" + refusal.Summary(),
		Summary:     refusal.Summary(),
		Detail:      refusal.Error(),
		Fields: map[string]any{
			"surface":        surface,
			"kind":           refusal.Kind,
			"db":             refusal.DBPath,
			"db_version":     refusal.DBVersion,
			"binary_version": refusal.BinaryVersion,
			"binary":         refusal.Binary,
		},
	})
	return true
}

// UpgradeTarget is the database `endless db upgrade` may operate on, or why it
// may not: the E-1429 worktree gate and the ED-1601 worktree-build refusal,
// exactly as DB() applies them — and NOTHING else of DB(). The version check
// and the integrity gates are what upgrade exists to get past, so it opens the
// file itself (schema.OpenExisting) rather than through here.
func UpgradeTarget() (string, error) {
	if err := guardWorktreeDBContext(); err != nil {
		return "", err
	}
	path := DBPath()
	if refusesMainDB(candidateBuild(), isMainDB(path)) {
		return "", worktreeBuildRefusal(path)
	}
	return path, nil
}
