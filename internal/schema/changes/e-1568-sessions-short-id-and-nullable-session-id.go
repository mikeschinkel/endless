//go:build ignore

// E-1568: add sessions.short_id and relax sessions.session_id to nullable, to
// support background-agent (kind_id=2) rows.
//
// Background agents are dispatched via `claude --bg`, which returns only a
// short id (the ~8-hex dispatch handle) at launch — the real session UUID does
// not exist until the bg agent's SessionStart hook fires. So a bg row is
// inserted with session_id NULL + short_id set, and SessionStart later UPDATEs
// session_id to the real UUID (keyed by short_id). That requires:
//
//   1. short_id TEXT — new, unique where NOT NULL.
//   2. session_id TEXT — drop NOT NULL (keep UNIQUE; SQLite treats NULLs as
//      distinct, so multiple pending bg rows coexist).
//
// (1) is a plain ADD COLUMN, but (2) needs a full table rebuild: SQLite has no
// `ALTER TABLE ... ALTER COLUMN DROP NOT NULL`. The rebuild is authored as a
// .go change (not .sql) for the same reason as e-1571: the recommended rebuild
// runs with PRAGMA foreign_keys=OFF, which the .sql dispatcher cannot do (it
// executes on monitor.DB() with FK ON, inside BEGIN IMMEDIATE, where PRAGMA
// foreign_keys is a documented no-op). The runner opens its own connection with
// foreign_keys at SQLite's default (OFF), so DROP TABLE sessions does not
// cascade to session_messages, and the FK from session_messages ->
// sessions(session_id) re-resolves by name after the RENAME. FK enforcement
// resumes when the app reopens the DB with foreign_keys=ON.
//
// Runs once, at land time (`just land`), against the populated real DB. The
// sandbox (empty mode) and tests build from schema.sql, which declares the
// post-rebuild shape directly and never applies change files.
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
			// Rebuilt table: session_id nullable, short_id added. Column set
			// and order otherwise identical to the pre-change sessions table
			// so the INSERT...SELECT below maps 1:1.
			`CREATE TABLE sessions_new (
				id INTEGER PRIMARY KEY,
				session_id TEXT,
				project_id INTEGER,
				platform TEXT NOT NULL DEFAULT 'claude',
				state TEXT NOT NULL DEFAULT 'working',
				active_task_id INTEGER,
				active_epic_id INTEGER,
				kind_id INTEGER NOT NULL DEFAULT 1,
				plan_file_path TEXT,
				process TEXT,
				started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
				last_activity TEXT,
				transcript_offset INTEGER NOT NULL DEFAULT 0,
				transcript_path TEXT,
				summary TEXT,
				hidden INTEGER NOT NULL DEFAULT 0,
				needs_recap INTEGER NOT NULL DEFAULT 0,
				summary_seq INTEGER NOT NULL DEFAULT 0,
				short_id TEXT,
				UNIQUE (session_id),
				FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
				FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
				FOREIGN KEY (active_epic_id) REFERENCES tasks(id) ON DELETE SET NULL,
				FOREIGN KEY (kind_id) REFERENCES session_kinds(id)
			)`,
			// Copy every existing row; short_id defaults NULL for all of them.
			`INSERT INTO sessions_new (
				id, session_id, project_id, platform, state, active_task_id,
				active_epic_id, kind_id, plan_file_path, process, started_at,
				last_activity, transcript_offset, transcript_path, summary,
				hidden, needs_recap, summary_seq
			)
			SELECT
				id, session_id, project_id, platform, state, active_task_id,
				active_epic_id, kind_id, plan_file_path, process, started_at,
				last_activity, transcript_offset, transcript_path, summary,
				hidden, needs_recap, summary_seq
			FROM sessions`,
			`DROP TABLE sessions`,
			`ALTER TABLE sessions_new RENAME TO sessions`,
			// DROP TABLE took the triggers with it; recreate them (E-1530).
			`CREATE TRIGGER sessions_null_process_on_end_update
				AFTER UPDATE OF state ON sessions
				WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
				BEGIN
					UPDATE sessions SET process = NULL WHERE id = NEW.id;
				END`,
			`CREATE TRIGGER sessions_null_process_on_end_insert
				AFTER INSERT ON sessions
				WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
				BEGIN
					UPDATE sessions SET process = NULL WHERE id = NEW.id;
				END`,
			`CREATE UNIQUE INDEX idx_sessions_short_id_unique
				ON sessions(short_id) WHERE short_id IS NOT NULL`,
		}
		for _, s := range stmts {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		return nil
	})
}
