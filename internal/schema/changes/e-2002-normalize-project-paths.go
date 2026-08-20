//go:build ignore

// E-2002: rewrite every projects.path to its canonical form and fold away the
// duplicate rows the unresolved-path bug created.
//
// The code half of E-2002 made the Go hook and the Python CLI normalize a
// project path the same way. This is the data half: a ledger written while they
// disagreed holds rows in the old spelling, and — wherever the hook won the race
// to a directory the CLI had already registered — a second, auto-registered row
// for that same directory.
//
// Both are fixed here rather than tolerated forever. `projects.path` is UNIQUE,
// so rewriting the second row's path to the first's would fail; the merge is
// therefore not optional extra credit, it is what makes the rewrite possible at
// all. RepairProjectPaths repoints every column that references the duplicate at
// the survivor before deleting it, so the sessions and tasks that accumulated
// against the twin are kept, not cascaded away.
//
// A `.go` change rather than `.sql` because the canonical form of a path is a
// filesystem question — it depends on which components are symlinks on THIS
// machine — and SQLite cannot answer it. The logic lives in
// monitor.RepairProjectPaths so it can be tested (a `//go:build ignore` script
// is invisible to `go test`); this file is the thin caller.
//
// Idempotent by construction, on top of the _schema_version marker: a second run
// finds every path already canonical and every group a single row, and changes
// nothing.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` names the file explicitly and so
// runs it regardless.
package main

import (
	"database/sql"
	"log"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		repair, err := monitor.RepairProjectPaths(tx)
		if err != nil {
			return err
		}
		// Logged even at zero. This repair edits rows a user cares about, and
		// silence would leave no way to tell "nothing needed fixing" apart from
		// "the change never ran".
		log.Printf(
			"e-2002: %d project path(s) rewritten to canonical form, %d duplicate row(s) merged",
			repair.Rewritten, repair.Merged,
		)
		return nil
	})
}
