//go:build ignore

// E-2011: rewrite every projects.path to the home-relative form.
//
// E-2002 fixed the two halves of Endless to normalize a project path
// identically and rewrote the column to that form: absolute, symlinks
// resolved. ED-1562 was then amended — the canonical form is home-relative,
// `~/Projects/acme`, absolute only outside $HOME — because `endless sql` is a
// supported surface and an ad-hoc query over the database is far easier to read
// without a column of identical home prefixes. This is the data half of that
// re-pointing: a database repaired by E-2002 is now in the previous canonical
// form, and this brings it to the current one.
//
// The work is the same work, so the code is the same code: this calls
// monitor.RepairProjectPaths, exactly as e-2002-normalize-project-paths.go
// does. "Canonical" is defined in exactly one place (monitor.StoredProjectPath),
// so whichever of the two changes a given DB has already applied, running this
// leaves every row in the spelling the current build calls canonical — and the
// duplicate-merging half stays available for a DB upgrading straight past
// E-2002 without ever having run it.
//
// A `.go` change rather than `.sql` because the canonical form of a path is a
// filesystem question — which components are symlinks on THIS machine, and
// where THIS user's $HOME is — and SQLite cannot answer it.
//
// Idempotent by construction, on top of the _schema_version marker: a second
// run finds every path already canonical and changes nothing.
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
			"e-2011: %d project path(s) rewritten home-relative, %d duplicate row(s) merged",
			repair.Rewritten, repair.Merged,
		)
		return nil
	})
}
