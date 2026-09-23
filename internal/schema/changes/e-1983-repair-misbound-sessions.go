//go:build ignore

// E-1983: unbind the `sessions` rows a reused spawn window mis-bound.
//
// `endless task spawn` sets @endless_task_id on the tmux window it creates and
// nothing ever clears it, so a window that outlived its session handed that task
// to whatever Claude session started in it next. E-1983 removed the mechanism —
// the working directory is now the only thing that binds — and this puts the
// rows it already wrote back.
//
// The discriminator is the LAUNCH DIRECTORY, taken from the FIRST hook event
// each session recorded (`activity.working_dir`). It is the only durable record
// of where a `claude` was started, and it must be the FIRST one: a `/cd` changes
// a session's per-line cwd without moving the session, a trap that already made
// one reading of this data mistake two separate sessions for one session's
// clears. The judgment itself lives in monitor.RepairMisboundSessions, which
// states the three verdicts and why each is safe; this file is the thin caller,
// following e-1967's shape.
//
// WHY THE TRIGGER IS DROPPED AND RE-CREATED AROUND IT. `sessions.task_id` is
// write-once (ED-1560, enforced by sessions_task_id_write_once), and the trigger
// aborts a non-NULL -> NULL write as readily as a reassignment. So the repair
// cannot run underneath it. Dropping it, repairing, and re-creating it inside ONE
// transaction is the same drop-repair-recreate shape e-1969's change file
// already uses around that trigger: it is never absent outside the transaction,
// and NO verb gains a runtime bypass — write-once stays absolute for every
// caller.
//
// The ledger does not undo this. `sessions` is machine-local runtime state, not
// a projection: internal/events/projector.go has cases for task and decision
// events only, so `rebuild-db` will neither replay the mis-binds nor revert the
// repair. e-1967 records the same fact for the same table.
//
// Nothing is folded in silently. Every task in the population gets a printed
// verdict, including the ones deliberately left alone: rows that are all one
// session's instances (E-2063's population, not this bug) and rows whose launch
// directories disagree with no row in the task's own worktree, where there is no
// evidence for which binding is genuine and a guess could destroy the only one a
// task has.
//
// Idempotent on top of the _schema_version marker: a second run finds the
// mis-bound rows already NULL, so they no longer belong to any task's row set
// and nothing changes.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` names the file explicitly and so
// runs it regardless.
package main

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

// writeOnceTrigger is the ED-1560 enforcement, byte-for-byte what
// internal/schema/schema.sql declares for a fresh database. The two must not
// drift (ED-1472), so if you edit one, edit the other.
const writeOnceTrigger = `CREATE TRIGGER IF NOT EXISTS sessions_task_id_write_once
BEFORE UPDATE OF task_id ON sessions
WHEN OLD.task_id IS NOT NULL AND NEW.task_id IS NOT OLD.task_id
BEGIN
    SELECT RAISE(ABORT, 'sessions.task_id is write-once');
END`

func main() {
	runner.Run(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			"DROP TRIGGER IF EXISTS sessions_task_id_write_once",
		); err != nil {
			return fmt.Errorf("dropping write-once trigger before repair: %w", err)
		}

		repairs, err := monitor.RepairMisboundSessions(tx)
		if err != nil {
			return err
		}

		unbound := 0
		for _, r := range repairs {
			log.Printf("e-1983: %s", r)
			unbound += len(r.Unbound)
		}
		// Logged even at zero: this repair edits rows a user cares about, and
		// silence would leave no way to tell "nothing needed fixing" apart from
		// "the change never ran".
		log.Printf("e-1983: %d session row(s) unbound across %d task(s) examined",
			unbound, len(repairs))

		// Put it back INSIDE the transaction, so write-once is never absent to
		// any other connection.
		if _, err = tx.Exec(writeOnceTrigger); err != nil {
			return fmt.Errorf("re-creating write-once trigger after repair: %w", err)
		}
		return nil
	})
}
