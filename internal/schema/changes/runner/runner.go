// Package runner is the tiny helper that `.go` schema-change scripts import.
//
// A `.go` change under internal/schema/changes/ is a `package main` program
// whose main() hands its work to Run as a callback:
//
//	package main
//
//	import (
//	    "database/sql"
//	    "github.com/mikeschinkel/endless/internal/schema/changes/runner"
//	)
//
//	func main() {
//	    runner.Run(func(tx *sql.Tx) error {
//	        // do work using tx
//	        return nil
//	    })
//	}
//
// Run opens the DB, derives the change name from the program name, gates on
// the _schema_version marker, wraps the callback in a BEGIN IMMEDIATE
// transaction, records the marker on success, and exits the process with the
// right status. There is no registry and no init() registration: the only
// thing that knows a change exists is its file on disk.
//
// Two programs compile and run these scripts, and neither is the application:
// `endless-go event apply-change` (the installed binary, and the only path
// outside self_dev) and ED-1571's cmd/endless-migrate, which a self_dev land
// uses because ED-1567 forbids its candidate endless-go migrating the real
// ledger. Both pass the database they resolved through
// schemachange.ChangeDBEnvVar, so the script never picks a file of its own.
package runner

import (
	"database/sql"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/dbcontext"
	"github.com/mikeschinkel/endless/internal/schemachange"
)

// Run applies one .go change inside a transaction and exits the process.
// On success (callback returns nil) it inserts the change's _schema_version
// marker and COMMITs, then exits 0. On failure it ROLLBACKs and exits 1. If
// the change is already recorded, it logs and exits 0 without re-running.
func Run(apply func(*sql.Tx) error) {
	name := changeName()

	// _txlock=immediate makes db.Begin() issue BEGIN IMMEDIATE so a concurrent
	// writer blocks on the RESERVED lock rather than racing the change.
	db, err := sql.Open("sqlite", "file:"+dbPath()+"?_txlock=immediate")
	if err != nil {
		fail(name, "open db", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err = db.Exec(schemachange.VersionTableDDL); err != nil {
		fail(name, "ensure _schema_version", err)
	}

	var applied int
	db.QueryRow("SELECT count(*) FROM _schema_version WHERE name = ?", name).Scan(&applied)
	if applied > 0 {
		log.Printf("apply-change: %q already applied; skipping", name)
		os.Exit(0)
	}

	tx, err := db.Begin()
	if err != nil {
		fail(name, "begin", err)
	}

	if err = apply(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			log.Printf("apply-change: %q rollback failed: %v", name, rbErr)
		}
		fail(name, "apply", err)
	}

	if _, err = tx.Exec("INSERT INTO _schema_version (name) VALUES (?)", name); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			log.Printf("apply-change: %q rollback failed: %v", name, rbErr)
		}
		fail(name, "record marker", err)
	}

	if err = tx.Commit(); err != nil {
		fail(name, "commit", err)
	}

	log.Printf("apply-change: %q applied", name)
	os.Exit(0)
}

// dbPath is the DB the change writes to. Whichever program applies the change
// passes the path it resolved via ENDLESS_CHANGE_DB, so this subprocess targets
// the exact same file (honoring any ForceRealDB redirect) instead of resolving
// one of its own. A developer running the script directly falls back to the
// default location.
//
// The default comes from internal/dbcontext rather than internal/monitor, so a
// compiled change script links the migration machinery and nothing else — the
// same property ED-1571's executable rests on, and for the same reason: a
// migration must not carry code that expects a schema. The answer is identical
// either way; monitor's extra routing (the hook pin, cwd sandbox detection, the
// --config-dir flag) is all set by callers this process does not have.
func dbPath() string {
	if p := os.Getenv(schemachange.ChangeDBEnvVar); p != "" {
		return p
	}
	return string(dbcontext.DBPath(dt.DirPath("")))
}

// changeName derives the marker key from the program name. `go run
// internal/schema/changes/e-NNN-slug.go` compiles to a temp binary named
// "e-NNN-slug", so the key matches the source basename without extension — and
// it is computed by the same function the applying program uses on the source
// path, which is what makes the two agree by construction rather than by
// coincidence.
func changeName() string {
	return schemachange.Name(dt.Filepath(os.Args[0]))
}

func fail(name, phase string, err error) {
	log.Printf("apply-change: %q %s: %v", name, phase, err)
	os.Exit(1)
}
