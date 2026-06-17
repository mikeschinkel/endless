//go:build ignore

// E-1571: add sessions.active_epic_id and sessions.kind_id to support
// background-agent rows and epic/child window-name rendering.
//
// Authored as a .go change (not .sql) on purpose. SQLite forbids
// `ALTER TABLE ... ADD COLUMN ... NOT NULL DEFAULT <non-null> REFERENCES ...`
// while foreign_keys is ON, and the table-rebuild workaround is unavailable
// here: four tables FK-reference sessions and the recommended rebuild requires
// PRAGMA foreign_keys=OFF *outside* a transaction, which the .sql dispatcher
// (running on monitor.DB() with FK ON, inside BEGIN IMMEDIATE) cannot do. The
// runner opens its own connection with foreign_keys at SQLite's default (OFF),
// so the full kind_id ALTER — REFERENCES + NOT NULL DEFAULT 1 — is permitted.
// The FK definition persists in the table schema and is enforced once the app
// reopens the DB with foreign_keys=ON (matching how tasks.type_id behaves).
//
// active_epic_id: nullable FK to tasks(id). Holds the epic task id when a
// session works under an epic (active_task_id then tracks the viewed child).
// NULL for non-epic sessions. Drives the [E-epic:E-child] status-line prefix.
//
// kind_id: FK to the session_kinds enum mirror (SessionKind Go enum is the
// source of truth per ED-1506). 'tmux' (1) = pane-bound session; 'background'
// (2) = headless agent with NULL process. All existing rows take the default 1.
//
// Runs once, at land time (`just land`), against the populated real DB. The
// sandbox (empty mode) and tests build from schema.sql, which declares the
// post-migration shape directly and never applies change files.
//
// The //go:build ignore tag keeps this file out of `go build/vet/test ./...`
// (it is a one-off `package main` script); `go run <path>` — which the
// apply-change dispatcher uses — still executes it.
package main

import (
	"database/sql"

	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		stmts := []string{
			`CREATE TABLE IF NOT EXISTS session_kinds (
				id    INTEGER PRIMARY KEY,
				slug  TEXT UNIQUE NOT NULL,
				label TEXT NOT NULL
			)`,
			`INSERT OR IGNORE INTO session_kinds (id, slug, label) VALUES
				(1, 'tmux',       'Tmux'),
				(2, 'background', 'Background')`,
			`ALTER TABLE sessions ADD COLUMN active_epic_id INTEGER REFERENCES tasks(id)`,
			// REFERENCES + NOT NULL DEFAULT 1 is only legal here because the
			// runner's connection has foreign_keys OFF (see file header).
			`ALTER TABLE sessions ADD COLUMN kind_id INTEGER NOT NULL DEFAULT 1 REFERENCES session_kinds(id)`,
			// No backfill UPDATE needed: existing rows take the default 1 (tmux).
		}
		for _, s := range stmts {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		return nil
	})
}
