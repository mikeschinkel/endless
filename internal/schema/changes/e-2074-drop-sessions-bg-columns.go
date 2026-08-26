//go:build ignore

// E-2074: drop four dead columns from `sessions` and the `session_kinds` table.
//
// The columns and why each one goes:
//
//   - kind_id  — the tmux/background discriminator (E-1571). Background agents
//     are removed by this same task, so every surviving row is
//     the one kind there is. Its FK target, session_kinds, goes
//     with it, along with the internal/sessionkind Go enum.
//   - short_id — the `claude --bg` dispatch handle (E-1568). Its only writers
//     and readers were the background-agent dispatch, decoration
//     and attach paths, all removed here.
//   - summary  — a 200-character slice of the first assistant response.
//     E-1925 replaces it with an on-demand recap. The one behavior
//     that rode along with it — auto-hiding a session whose first
//     response is a "Not logged in"/"Error:" greeting — survives as
//     monitor.hideIfErrorGreeting, which needs no stored column.
//   - plan_file_path — written at PostToolUse/Write, read only by
//     ExitPlanMode, which already carried an mtime scan of
//     ~/.claude/plans as the fallback for every session that
//     reached it without writing through the Write tool. With
//     auto-import disabled since E-1288, neither branch consumed
//     the path for anything but a presence test the directory
//     answers directly.
//
// A full table REBUILD rather than four `ALTER TABLE ... DROP COLUMN`s, because
// SQLite refuses DROP COLUMN on a column that participates in a UNIQUE
// constraint or a foreign key — `UNIQUE (short_id)` and
// `FOREIGN KEY (kind_id) REFERENCES session_kinds(id)` are both inline table
// constraints, so short_id and kind_id are undroppable in place. Rebuilding
// once is also cheaper than two ALTERs plus a rebuild, and it is the only way
// to drop the UNIQUE (short_id) index that would otherwise outlive its column.
//
// Authored as a .go change (not .sql) for the same reason as e-1568 and e-1571,
// the two prior sessions rebuilds: the recommended rebuild runs with
// PRAGMA foreign_keys=OFF, which the .sql dispatcher cannot arrange (it
// executes on monitor.DB() with FK ON, inside BEGIN IMMEDIATE, where PRAGMA
// foreign_keys is a documented no-op). The runner opens its own connection at
// SQLite's default (OFF), so `DROP TABLE sessions` does not cascade into the
// eight tables that reference it — session_messages, session_navigations,
// session_gates, session_tasks, session_statuses and the rest — and each of
// those foreign keys re-resolves by name after the RENAME. FK enforcement
// resumes when the app next opens the DB with foreign_keys=ON.
//
// Runs once, at land time (`just land`), against the populated real DB where
// the columns still exist. The sandbox (`endless-sandbox init`) and the tests
// build from schema.sql, which declares the post-rebuild shape directly and
// never applies change files — so there is no "column absent" path to guard.
//
// session_id stays NULLable. E-1568 relaxed it so a background agent could be
// recorded at dispatch before its UUID existed, and that reason dies here — but
// E-2063 is about to reuse the same nullability for harness instances, so
// re-tightening it now would be churn that E-2063 immediately undoes.
//
// The e-1568 and e-1571 change files still CREATE these columns when they
// rebuild a pre-E-1568 sessions table. That is correct and deliberately
// untouched: each reproduces a historical shape against a DB that predates this
// change, and this file then drops them. Editing history there would break
// their verify scripts, which build those old shapes inline.
//
// The //go:build ignore tag keeps this one-off `package main` script out of
// `go build/vet/test ./...`; `go run <path>` (the apply-change dispatcher) still
// executes it.
package main

import (
	"database/sql"

	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		stmts := []string{
			// PRAGMA legacy_alter_table=ON for the duration of the rebuild.
			//
			// Without it, `ALTER TABLE sessions_new RENAME TO sessions` reparses
			// every trigger and view in the schema to fix up references to the
			// renamed table — and two triggers on OTHER tables reference
			// sessions by name (tasks_notify_sessions and
			// task_landings_notify_sessions, which INSERT a notice row per
			// session holding the changed task). At the moment of the rename the
			// old `sessions` has already been dropped, so that reparse fails
			// with `error in trigger tasks_notify_sessions: no such table:
			// main.sessions` and the whole change rolls back.
			//
			// Legacy mode is exactly right here rather than a workaround: the
			// references those triggers hold are ALREADY the name we are
			// renaming TO, so there is nothing to fix up. Turning the fix-up off
			// leaves them correct; leaving it on breaks on a table that is
			// mid-swap. Scoped to this transaction and restored below.
			`PRAGMA legacy_alter_table = ON`,
			// The post-change shape, byte-for-byte the sessions table declared
			// in internal/schema/schema.sql minus the `IF NOT EXISTS`. Keeping
			// the two in sync is what makes a migrated DB and a fresh one the
			// same DB.
			`CREATE TABLE sessions_new (
				id INTEGER PRIMARY KEY,
				session_id TEXT,
				project_id INTEGER,
				platform TEXT NOT NULL DEFAULT 'claude',
				state TEXT NOT NULL DEFAULT 'working',
				task_id INTEGER,
				epic_id INTEGER,
				process_id INTEGER,
				started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
				last_activity TEXT,
				transcript_offset INTEGER NOT NULL DEFAULT 0,
				hidden INTEGER NOT NULL DEFAULT 0,
				last_user_prompt TEXT,
				report_bounces INTEGER NOT NULL DEFAULT 0,
				report_exempt INTEGER NOT NULL DEFAULT 0,
				report_runs INTEGER NOT NULL DEFAULT 0,
				UNIQUE (session_id),
				FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
				FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL,
				FOREIGN KEY (epic_id) REFERENCES tasks(id) ON DELETE SET NULL,
				FOREIGN KEY (process_id) REFERENCES processes(id)
			)`,
			// Every column named explicitly on both sides. The four dropped
			// columns are simply absent; `id` is carried so every foreign key
			// pointing at sessions(id) still resolves to the same row.
			`INSERT INTO sessions_new (
				id, session_id, project_id, platform, state, task_id, epic_id,
				process_id, started_at, last_activity, transcript_offset, hidden,
				last_user_prompt, report_bounces, report_exempt, report_runs
			)
			SELECT
				id, session_id, project_id, platform, state, task_id, epic_id,
				process_id, started_at, last_activity, transcript_offset, hidden,
				last_user_prompt, report_bounces, report_exempt, report_runs
			FROM sessions`,
			`DROP TABLE sessions`,
			`ALTER TABLE sessions_new RENAME TO sessions`,
			// DROP TABLE took the trigger with it. Recreate it verbatim from
			// schema.sql — without this, sessions.task_id silently stops being
			// write-once (ED-1560 / E-1969) on every already-populated DB, which
			// is exactly the class of regression a rebuild is prone to.
			`CREATE TRIGGER sessions_task_id_write_once
				BEFORE UPDATE OF task_id ON sessions
				WHEN OLD.task_id IS NOT NULL AND NEW.task_id IS NOT OLD.task_id
				BEGIN
					SELECT RAISE(ABORT, 'sessions.task_id is write-once');
				END`,
			// Last, and only now: the FK that pinned it is gone with the old
			// table. Dropped rather than left behind because a mirror table
			// whose Go enum no longer exists cannot be integrity-checked, and an
			// unchecked enum mirror is worse than no mirror.
			`DROP TABLE IF EXISTS session_kinds`,
			// Restore the default. The connection is the runner's own and is
			// closed right after, but a change file must not leave a pragma set
			// for whatever runs next on it.
			`PRAGMA legacy_alter_table = OFF`,
		}
		for _, stmt := range stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	})
}
