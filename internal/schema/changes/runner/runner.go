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
// thing that knows a change exists is its file on disk, applied at land time
// by `endless db apply-change`.
package runner

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// schemaVersionDDL matches the shape in internal/schema/schema.sql. Created
// defensively so a `.go` change run against a DB that has never had schema.SQL
// applied still has somewhere to record its marker.
const schemaVersionDDL = `CREATE TABLE IF NOT EXISTS _schema_version (
	name       TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
)`

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

	if _, err = db.Exec(schemaVersionDDL); err != nil {
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

// dbPath is the DB the change writes to. The apply-change dispatcher passes the
// resolved path via ENDLESS_CHANGE_DB so the subprocess targets the exact same
// file (honoring any ForceRealDB redirect). A developer running the script
// directly falls back to the default location.
func dbPath() string {
	if p := os.Getenv("ENDLESS_CHANGE_DB"); p != "" {
		return p
	}
	return monitor.DBPath()
}

// changeName derives the marker key from the program name. `go run
// internal/schema/changes/e-NNN-slug.go` compiles to a temp binary named
// "e-NNN-slug", so the key matches the source basename without extension —
// the same key the dispatcher computes for .sql changes.
func changeName() string {
	base := filepath.Base(os.Args[0])
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func fail(name, phase string, err error) {
	log.Printf("apply-change: %q %s: %v", name, phase, err)
	os.Exit(1)
}
