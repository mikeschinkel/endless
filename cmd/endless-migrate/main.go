// Command endless-migrate applies Endless's schema changes, and does nothing
// else.
//
// ED-1571's third thing. In self_dev a candidate binary and an installed binary
// coexist against one real ledger, and during the pre-land window neither of
// them may migrate it: the candidate must not (ED-1567 — an unlanded build
// mutating the production schema is the 2026-08-10 outage exactly), and the
// installed one does not carry the change being landed. `worktree land` used to
// square that circle by handing the apply step the WORKTREE's own endless-go,
// whose embedded schema matches the rows the land just wrote (E-1664) — but that
// binary is a candidate by definition, so the invariant and the prohibition
// point opposite ways.
//
// This executable is the way out. It is compiled from the landing branch, so it
// carries that branch's change set; it opens the database FILE directly, so it
// never reaches the application's connect; and it has no schema of its own to
// apply, no enum mirror to verify and no business data to read, so there is
// nothing about the database it can expect and therefore nothing the database
// can disappoint. That is the whole of its safety, and it is why it is allowed
// where a candidate endless-go is not.
//
// Usage:
//
//	endless-migrate [--config-dir <dir>] apply <change-file>
//
// There is one subcommand, and the absence of the others is a property rather
// than an omission: no hook, no task, no event, no query, no tmux, no schema
// application. internal/schemachange/executable_test.go asserts both halves —
// the surface this exposes, and that its build links nothing but the migration
// machinery.
//
// Scope is self_dev ONLY. Every other project has one installed binary and no
// land at all, so it carries and applies its own migrations under ED-1570
// through `endless-go event apply-change`; no separate executable exists there
// and nothing here assumes one does.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/dbcontext"
	"github.com/mikeschinkel/endless/internal/schemachange"
)

// result is the JSON document an apply prints on stdout. It is
// schemachange.Result — the same shape `endless-go event apply-change` prints,
// so a caller parses one document whichever program it invoked — plus the
// database that was actually opened, because a migration tool that does not say
// which file it changed is asking to be trusted about the one thing worth
// checking.
type result struct {
	schemachange.Result
	DB dt.Filepath `json:"db"`
}

func main() {
	args, explicit, _ := dbcontext.ConsumeConfigDirFlag(os.Args)

	if len(args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch args[1] {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return
	case "apply":
		res, err := runApply(args[2:], explicit)
		if err != nil {
			emitError(res.Name, err)
		}
		emit(res)
		return
	}

	fmt.Fprintf(os.Stderr, "endless-migrate: unknown command %q\n\n", args[1])
	usage(os.Stderr)
	os.Exit(2)
}

// runApply applies exactly one change file to exactly one database.
//
// One file per invocation, matching what `worktree land` has always done: a
// change set can be several files, one can apply and the next fail, and the
// caller needs to know which — so it names them one at a time and reports each.
func runApply(args []string, explicit dt.DirPath) (res result, err error) {
	var db *sql.DB
	var path dt.Filepath
	var exists bool

	if len(args) != 1 {
		err = errUsage("apply requires exactly one <change-file>")
		goto end
	}

	path, err = dt.Filepath(args[0]).Abs()
	if err != nil {
		err = fmt.Errorf("resolving %s: %w", args[0], err)
		goto end
	}

	res.DB = dbcontext.DBPath(explicit)

	// An absolute database or nothing. dbcontext resolves a RELATIVE path when
	// no home directory and no XDG_CONFIG_HOME can be found, and a relative one
	// would be created under whatever directory this happened to be invoked
	// from — a fresh, empty database that migrates flawlessly and is not the
	// ledger anyone meant. Refusing is the only honest answer.
	if !res.DB.IsAbs() {
		err = fmt.Errorf(
			"refusing to migrate a database at a relative path: %s\n"+
				"Neither --config-dir nor XDG_CONFIG_HOME nor a home directory "+
				"resolved, so there is no way to know which ledger was meant.",
			res.DB)
		goto end
	}

	// The database must already exist. sql.Open would create one, and a
	// migration that CREATES its target has migrated nothing — it has
	// manufactured an empty file and reported success. A land reaches here only
	// after backing the real ledger up, so a missing file means the path is
	// wrong, not that the ledger is new.
	exists, err = res.DB.Exists()
	if err != nil {
		err = fmt.Errorf("checking for the database at %s: %w", res.DB, err)
		goto end
	}
	if !exists {
		err = fmt.Errorf("no database at %s", res.DB)
		goto end
	}

	db, err = openDB(res.DB)
	if err != nil {
		goto end
	}
	defer closeDB(db)

	res.Result, err = schemachange.Apply(db, res.DB, path, os.Stderr)

end:
	return res, err
}

// openDB opens the database file directly: no schema application, no enum seed,
// no integrity gate, no ownership or candidate check, no sandbox routing.
//
// This is the line that separates this executable from `endless-go event
// apply-change`, which opens through internal/monitor and therefore brings the
// application's whole connect with it. Everything omitted here is something that
// would make this tool's behaviour depend on the schema it is about to change.
//
// The three PRAGMAs are kept because they configure the CONNECTION rather than
// the schema, and internal/monitor sets exactly these three. A change file must
// be applied under the same connection settings whichever program applies it —
// foreign_keys above all, since a change that rewrites a table relies on
// enforcement being where it has always been.
func openDB(dbPath dt.Filepath) (db *sql.DB, err error) {
	var pragma string

	db, err = sql.Open("sqlite", string(dbPath))
	if err != nil {
		err = fmt.Errorf("opening %s: %w", dbPath, err)
		goto end
	}

	// sql.Open is lazy, so nothing above has touched the file yet.
	err = db.Ping()
	if err != nil {
		err = fmt.Errorf("connecting to %s: %w", dbPath, err)
		goto end
	}

	// SQLite is single-writer; one connection is what makes BEGIN IMMEDIATE
	// mean what it says through Go's connection pool.
	db.SetMaxOpenConns(1)

	for _, pragma = range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		_, err = db.Exec(pragma)
		if err != nil {
			err = fmt.Errorf("%s on %s: %w", pragma, dbPath, err)
			goto end
		}
	}

end:
	if err != nil && db != nil {
		closeDB(db)
		db = nil
	}
	return db, err
}

// closeDB reports a close failure on stderr rather than swallowing it. It cannot
// change the outcome — the work has already committed or rolled back — but a
// database that would not close is worth saying out loud once.
func closeDB(db *sql.DB) {
	err := db.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-migrate: closing the database: %v\n", err)
	}
}

// errUsage is a failure in how this was invoked rather than in what it was asked
// to do. It exits 2, as the dispatch above does, so a caller can tell "you asked
// wrong" from "it did not work".
func errUsage(msg string) error {
	fmt.Fprintf(os.Stderr, "endless-migrate: %s\n\n", msg)
	usage(os.Stderr)
	os.Exit(2)
	return nil
}

// emit prints the result document on stdout and exits 0.
func emit(res result) {
	b, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-migrate: encoding the result: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}

// emitError prints a failure as the same kind of document a success gets, on
// stdout, and exits 1 — the shape `endless-go event apply-change` established
// and the Python caller parses.
func emitError(name string, cause error) {
	out := map[string]any{"status": "error", "error": cause.Error()}
	if name != "" {
		out["name"] = name
	}
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-migrate: %v\n", cause)
		os.Exit(1)
	}
	fmt.Println(string(b))
	os.Exit(1)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "endless-migrate — apply Endless schema changes, and nothing else.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  endless-migrate [--config-dir <dir>] apply <change-file>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  apply <change-file>   Apply one internal/schema/changes/<name>.{sql,go}")
	fmt.Fprintln(w, "                        file and record it in _schema_version. Already")
	fmt.Fprintln(w, "                        applied changes are skipped.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --config-dir <dir>    The Endless config directory holding the database")
	fmt.Fprintln(w, "                        to migrate. Defaults to XDG_CONFIG_HOME/endless,")
	fmt.Fprintln(w, "                        else ~/.config/endless.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This binary carries the migration set and nothing else: it serves no hook,")
	fmt.Fprintln(w, "runs no task command, answers no query, and touches no business data outside")
	fmt.Fprintln(w, "a migration. That is what lets `endless worktree land` run it against the")
	fmt.Fprintln(w, "real ledger where an unlanded endless-go build may not.")
}
