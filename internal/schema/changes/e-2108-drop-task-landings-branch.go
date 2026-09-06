//go:build ignore

// E-2108: drop task_landings.branch.
//
// The column recorded the task branch a land came FROM. It existed because that
// name could not be derived: a task branch was `task/<id>-<title-slug>` with the
// slug frozen at worktree creation (E-971), so nothing holding a task id could
// reconstruct it. ED-1587 renamed task branches to `task/<id>`, making the name a
// pure function of the id — at which point a stored copy is a second source of
// truth for a fact the row's own task_id already determines.
//
// Every reader is gone before this runs:
//
//   - the worktree reaper (internal/monitor/reap_worktrees.go) read the latest
//     landing's branch to `git branch -D` it after removing the directory. It now
//     asks git what that directory has checked out, which is the authority on the
//     question and is right for a branch cut before the rename too. That also
//     retires E-1719's NULL carve-out (a record-only landing recorded no branch,
//     so the name was scanned as sql.NullString and the delete skipped) and
//     E-2087's git fallback for a worktree with no landing row — one code path
//     now, instead of a column, a null case and a fallback.
//   - `endless task landed` printed it in all three output modes. A landing's
//     interesting facts are when, which merge commit, and which branch it landed
//     ON (base_branch, E-2005) — that last one genuinely is not derivable, and
//     stays.
//
// base_branch is NOT affected. It answers the opposite question and no id
// determines it: a project may land into any branch.
//
// ORDERING: after this runs, an endless-go binary older than this change still
// INSERTs `branch` and every `task.landed` emit fails with "no such column".
// `endless worktree land` applies branch schema changes between the ff-merge and
// its own record-landing step, and `just land` rebuilds the binaries immediately
// after main advances, so the window is inside one land and closes without
// intervention. Landing first and installing after is the normal order; installing
// an unlanded build against the real DB is what would widen it.
//
// ALTER TABLE ... DROP COLUMN rather than the table rebuild e-1719 used, because
// SQLite can do it here: `branch` participates in no constraint, no index
// (idx_task_landings_task is (task_id, landed_at DESC)) and no trigger body
// (task_landings_notify_sessions reads base_branch, merge_commit_sha, task_id and
// session_id). A rebuild would additionally have to re-create that trigger, which
// DROP TABLE takes with it — easy to forget, and forgetting it silently loses the
// landed notice on every already-populated DB.
//
// What DROP COLUMN costs instead, and it is worth knowing before writing another
// one: SQLite re-parses the WHOLE schema to prove the column is unreferenced, so
// it aborts on any view or trigger that cannot resolve — including ones with no
// connection to this table. On a DB missing `tasks.removed` it fails inside
// `live_tasks`, which schema.sql deliberately tolerates as a lazy no-op until
// e-1929 is applied. So this change needs the earlier changes applied first,
// which is the order `endless db apply-change` runs them in anyway. It is also
// why this task's verify suite builds its pre-change fixture from schema.sql
// rather than hand-rolling a miniature task_landings: nothing smaller than the
// real schema can reach the statement below.
//
// A `.go` change rather than `.sql` for the reason e-2005 is one: SQLite has no
// DROP COLUMN IF EXISTS, and a DB that never had the column would abort the
// change rather than no-op on it. That is every fresh install and every sandbox —
// they build from schema.sql, which no longer declares the column. The probe below
// makes the change a no-op there; the _schema_version marker gates re-runs on top
// of it.
//
// e-1719-nullable-task-landings-branch.sql, which rebuilt the table to make this
// column nullable, is deliberately KEPT. Change files are the historical record of
// how a populated database reached its shape, and a DB old enough to still need it
// applies relax-then-drop in that order, which is correct. Same treatment E-2081
// and E-2074 gave the change files whose objects they removed.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` (the apply-change dispatcher) names
// the file explicitly and runs it regardless.
package main

import (
	"database/sql"
	"fmt"

	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		var present int
		err := tx.QueryRow(
			`SELECT count(*) FROM pragma_table_info('task_landings')
			  WHERE name = 'branch'`,
		).Scan(&present)
		if err != nil {
			return fmt.Errorf("probing task_landings columns: %w", err)
		}
		if present == 0 {
			return nil
		}
		if _, err = tx.Exec(
			"ALTER TABLE task_landings DROP COLUMN branch",
		); err != nil {
			return fmt.Errorf("dropping task_landings.branch: %w", err)
		}
		return nil
	})
}
