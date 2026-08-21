//go:build ignore

// E-2005: add task_landings.base_branch and task_landings.landed_by_harness —
// the two facts the task_landings_notify_sessions trigger needs in order to
// tell a session its work landed, and to tell the right sessions.
//
// base_branch is the branch the work landed ON (`branch` is the one it landed
// FROM, which is the task branch). It is what the notice reads back: "E-2005
// landed on main (1dd0006)".
//
// landed_by_harness names the agent harness that ran the land — an agentenv.ID
// — or NULL when a PERSON ran it. It is the axis session_id cannot supply:
// session_id answers "which session is this about", and its resolver
// deliberately credits a bare shell in a sibling tmux pane to the agent beside
// it (E-1294), so a human's land routinely arrives carrying an agent's session
// id. Suppressing on session_id alone would silence exactly the session that
// needed to hear.
//
// Both nullable and additive, so schema.sql declares the post-migration shape
// and this file brings existing DBs up to it. CREATE TABLE IF NOT EXISTS no-ops
// on a populated DB, so a new column never reaches one without a change file.
//
// A `.go` change rather than `.sql` because SQLite has no ADD COLUMN IF NOT
// EXISTS: the probes below make this a no-op on a DB already built from
// schema.sql (fresh DBs, the sandbox, and tests all have both columns from the
// CREATE TABLE). The _schema_version marker gates re-runs on top of that.
//
// ORDERING (important): schema.sql's task_landings_notify_sessions trigger
// reads NEW.base_branch and NEW.landed_by_harness, and SQLite resolves a
// trigger body at FIRE time, not CREATE time — so on a populated DB, CREATE
// TRIGGER succeeds and every INSERT INTO task_landings then fails with "no such
// column" until this change is applied. Apply it at land (`endless worktree
// land` runs `endless db apply-change`) BEFORE the new binary becomes the
// deployed one via `just install`. Landing first is the normal order and leaves
// no window; installing an unlanded build against the main database is what would
// open one. E-1818 already bars a worktree binary pinned onto a real DB from
// applying schema.SQL at all, so a self-dev worktree cannot create the trigger
// on the real DB ahead of this change — which is also why the land that ships
// E-2005 records its own landing without firing the trigger it adds.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` names the file explicitly and so
// runs it regardless.
package main

import (
	"database/sql"
	"fmt"

	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		for _, col := range []string{"base_branch", "landed_by_harness"} {
			var present int
			err := tx.QueryRow(
				`SELECT count(*) FROM pragma_table_info('task_landings')
				  WHERE name = ?`, col,
			).Scan(&present)
			if err != nil {
				return fmt.Errorf("probing task_landings columns: %w", err)
			}
			if present > 0 {
				continue
			}
			// Column name is from the literal slice above, never user input, so
			// interpolating it into the DDL is safe — ALTER TABLE takes no
			// parameter binding for identifiers.
			if _, err := tx.Exec(
				"ALTER TABLE task_landings ADD COLUMN " + col + " TEXT",
			); err != nil {
				return fmt.Errorf("adding task_landings.%s: %w", col, err)
			}
		}
		return nil
	})
}
