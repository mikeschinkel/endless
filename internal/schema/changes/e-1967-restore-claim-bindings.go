//go:build ignore

// E-1967: restore the session→task bindings that the pre-ED-1560 code destroyed.
//
// `endless task spawn` now refuses a task any session ever claimed, and it reads
// that fact off `sessions.task_id`. The guard therefore protects only tasks whose
// claimant still HAS a binding — and for months `task reopen` cleared the binding
// in the same command that recorded a defect report against the landed work. The
// tasks most likely to be wrongly spawned onto are exactly the ones whose link
// was cut. This puts them back.
//
// The ledger is the only surviving evidence. `sessions` is machine-local runtime
// state, not a projection: internal/events/projector.go has cases for task and
// decision events only, with none for `task.claimed` or `task.released`. So
// `rebuild-db` will not undo this repair — and would never have performed it.
//
// The ED-1560 write-once trigger cannot fire on these writes. It aborts only
// when `OLD.task_id IS NOT NULL`, and every row this touches is one where the
// binding is NULL; `NULL -> value` is explicitly permitted. No exemption, no
// trigger drop, nothing to re-create afterwards.
//
// NOT part of this change: neutering `execTaskReleased`. An earlier revision of
// the plan called for it, believing a rebuild would replay `task.released` and
// re-destroy these bindings. It will not — see the projector note above. The
// executor is unreachable in practice and is left exactly as it is.
//
// A `.go` change rather than `.sql` because the evidence is in JSONL files on
// disk, one ledger per registered project, and SQLite cannot read them. The
// logic lives in events.RepairClaimBindings so it can be tested (a
// `//go:build ignore` script is invisible to `go test`); this file is the thin
// caller, following e-2002's shape.
//
// Idempotent by construction on top of the _schema_version marker: a second run
// finds every repairable session already bound, so no row is a candidate and
// nothing changes.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` names the file explicitly and so
// runs it regardless.
package main

import (
	"database/sql"
	"log"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		repair, err := events.RepairClaimBindings(tx)
		if err != nil {
			return err
		}
		// Logged even at zero: this repair edits rows a user cares about, and
		// silence would leave no way to tell "nothing needed fixing" apart from
		// "the change never ran".
		log.Printf(
			"e-1967: %d session binding(s) restored from %d project ledger(s); "+
				"%d skipped as ambiguous, %d naming a task that no longer exists",
			repair.Restored, repair.Projects, repair.Ambiguous, repair.MissingTask,
		)
		return nil
	})
}
