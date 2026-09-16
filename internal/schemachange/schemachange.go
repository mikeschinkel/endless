// Package schemachange applies one per-ticket schema-change file to an Endless
// database and records that it ran.
//
// A change is a file under internal/schema/changes/ named e-NNN-slug.{sql,go}.
// Its basename without the extension is its NAME, and the name is the whole of
// the bookkeeping: a row in _schema_version means "this change has run against
// this database", and a change whose row is already there is skipped rather than
// re-run. There is no ordered version and no registry — the only thing that
// knows a change exists is its file on disk.
//
// A change runs in FULL: the DDL it declares and the DML that goes with it —
// enum seed rows, backfills, the INSERT ... SELECT of a table rebuild. There is
// no DDL-only mode, and there could not be one: schema.sql seeds task_types,
// process_kinds and the other enum mirrors, and a database migrated without its
// seed rows fails the fail-closed integrity gates on the next connect. See
// ED-1571.
//
// This package is the one definition of what applying one change means, and it
// is deliberately free of everything else Endless is. Two programs apply
// changes, and they differ ONLY in how they opened the database:
//
//   - `endless-go event apply-change` opens through internal/monitor — the
//     application's connect, which applies schema.sql, seeds the enum mirrors and
//     runs the fail-closed integrity gates. That is the installed binary's path,
//     and the only one outside self_dev.
//   - cmd/endless-migrate (ED-1571) opens the database file directly, carrying no
//     schema of its own to apply and no enum table to check. In self_dev that is
//     the only permitted path at land time: the worktree's binary is a CANDIDATE
//     build and ED-1567 forbids a candidate migrating the real ledger, while the
//     installed binary does not yet carry the change being landed. A tool that
//     expects nothing of the schema cannot be broken by the schema.
//
// Because the difference between them is the HANDLE, this package takes one. It
// never opens a database and never decides which one to open.
package schemachange

import (
	"database/sql"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// ChangeDBEnvVar names the database a .go change script must open. The script
// runs as a subprocess and cannot see the handle its parent resolved, so the
// parent passes the path it actually opened — never letting the child resolve
// one of its own and land on a different file.
const ChangeDBEnvVar = "ENDLESS_CHANGE_DB"

// VersionTableDDL matches the _schema_version shape in
// internal/schema/schema.sql. Created defensively before the marker is read or
// written, so a change applied to a database that has never had schema.sql
// exec'd against it still has somewhere to record itself.
const VersionTableDDL = `CREATE TABLE IF NOT EXISTS _schema_version (
	name       TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
)`

// Status is what applying one change turned out to be.
type Status string

const (
	// StatusApplied means the change's effects and its marker committed together.
	StatusApplied Status = "applied"
	// StatusSkipped means _schema_version already held the change's marker.
	StatusSkipped Status = "skipped"
)

// Result reports what Apply did with one change file. Its JSON encoding is the
// document both applying programs print on stdout, so a caller parses one shape
// whichever program it invoked.
type Result struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Name is the marker key for the change file at path: its basename with the
// extension removed. `go run internal/schema/changes/e-NNN-slug.go` compiles to
// a temporary binary of that same basename, which is how a .go change's own
// marker key comes out equal to the one computed here.
func Name(path dt.Filepath) string {
	return strings.TrimSuffix(string(path.Base()), string(path.Ext()))
}

// EnsureVersionTable creates _schema_version when it is absent.
func EnsureVersionTable(db *sql.DB) (err error) {
	_, err = db.Exec(VersionTableDDL)
	if err != nil {
		err = doterr.NewErr(ErrEnsuringVersionTable, err)
	}
	return err
}

// Applied reports whether the change named name has already run against db.
func Applied(db *sql.DB, name string) (applied bool, err error) {
	var count int

	err = db.QueryRow("SELECT count(*) FROM _schema_version WHERE name = ?", name).Scan(&count)
	if err != nil {
		err = doterr.NewErr(ErrReadingMarker, "name", name, err)
		goto end
	}
	applied = count > 0

end:
	return applied, err
}

// Apply applies the change file at path to db and records its marker, and is
// idempotent: a change already recorded is reported StatusSkipped without
// running again.
//
// dbPath must be the file db is open on. A .sql change runs on the handle; a .go
// change runs as a subprocess and is told which database to open through
// ChangeDBEnvVar, so the two paths cannot diverge onto different files.
//
// logs receives a .go change's own output. Pass stderr: the applying program's
// stdout carries one JSON document and nothing else.
func Apply(db *sql.DB, dbPath dt.Filepath, path dt.Filepath, logs io.Writer) (res Result, err error) {
	var exists bool
	var applied bool

	res.Name = Name(path)

	exists, err = path.Exists()
	if err != nil {
		err = doterr.NewErr(ErrResolvingChangePath, err)
		goto end
	}
	if !exists {
		err = doterr.NewErr(ErrChangeNotFound)
		goto end
	}

	err = EnsureVersionTable(db)
	if err != nil {
		goto end
	}

	applied, err = Applied(db, res.Name)
	if err != nil {
		goto end
	}
	if applied {
		res.Status = StatusSkipped
		res.Reason = "already applied"
		goto end
	}

	// Lower-cased for the dispatch only: the extension decides HOW a change is
	// applied, and ".SQL" on a case-insensitive filesystem is the same change
	// as ".sql". Name() trims the extension as written, so the marker key is
	// unaffected either way.
	switch dt.FileExt(strings.ToLower(string(path.Ext()))) {
	case ".sql":
		err = applySQL(db, path, res.Name)
	case ".go":
		err = applyGo(path, dbPath, logs)
	default:
		err = doterr.NewErr(ErrUnsupportedChange, "ext", path.Ext())
	}
	if err != nil {
		goto end
	}
	res.Status = StatusApplied

end:
	if err != nil {
		err = doterr.WithErr(err, "change", res.Name, "path", path, "db", dbPath)
		err = doterr.NewErr(ErrApplyingChange, err)
	}
	return res, err
}

// applySQL runs a .sql change's statements and the marker insert in one
// BEGIN IMMEDIATE transaction on the handle.
//
// The marker insert runs AFTER the file's statements, against whatever shape the
// file leaves behind — a change is allowed to reshape _schema_version itself, as
// the E-1459 reshape does.
func applySQL(db *sql.DB, path dt.Filepath, name string) (err error) {
	var content []byte

	content, err = path.ReadFile()
	if err != nil {
		err = doterr.NewErr(ErrReadingChange, err)
		goto end
	}

	_, err = db.Exec("BEGIN IMMEDIATE TRANSACTION")
	if err != nil {
		err = doterr.NewErr(ErrBeginning, err)
		goto end
	}

	_, err = db.Exec(string(content))
	if err != nil {
		err = rollback(db, doterr.NewErr(ErrApplyingChange, err))
		goto end
	}

	_, err = db.Exec("INSERT INTO _schema_version (name) VALUES (?)", name)
	if err != nil {
		err = rollback(db, doterr.NewErr(ErrRecordingMarker, err))
		goto end
	}

	_, err = db.Exec("COMMIT")
	if err != nil {
		err = rollback(db, doterr.NewErr(ErrCommitting, err))
		goto end
	}

end:
	return err
}

// applyGo runs a .go change through `go run`. The script does its own
// BEGIN IMMEDIATE, work, marker insert and COMMIT through
// internal/schema/changes/runner, which is why nothing here wraps it in a
// transaction — two transactions on one database would deadlock the second.
//
// A failure returns an error rather than propagating the subprocess's exit
// status: the runner exits 0 or 1 and `go run` exits 1 on a compile failure, so
// there has never been a third code to carry.
func applyGo(path dt.Filepath, dbPath dt.Filepath, logs io.Writer) (err error) {
	cmd := exec.Command("go", "run", string(path))
	cmd.Env = append(os.Environ(), ChangeDBEnvVar+"="+string(dbPath))
	cmd.Stdout = logs
	cmd.Stderr = logs

	err = cmd.Run()
	if err != nil {
		err = doterr.NewErr(ErrRunningChangeScript, err)
	}
	return err
}

// rollback unwinds the open transaction and returns the error to report. A
// failed rollback does not replace the cause that prompted it — it is appended,
// because "the change failed" and "the database is still in a transaction" are
// two facts and the second one never explains the first.
func rollback(db *sql.DB, cause error) (err error) {
	var rbErr error

	err = cause

	_, rbErr = db.Exec("ROLLBACK")
	if rbErr != nil {
		err = doterr.CombineErrs([]error{cause, doterr.NewErr(ErrRollingBack, rbErr)})
	}
	return err
}
