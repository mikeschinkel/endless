//go:build ignore

// E-1898: replace sessions.process (a bare tmux pane id) with sessions.process_id
// pointing at a `processes` row whose identity is (kind, server_uuid, address).
//
// WHY. A tmux pane id is not unique over time. A tmux server restart reissues
// "%414" to an unrelated pane, so a session keyed on the pane string alone can
// resolve to the wrong session (E-1530) — and a sweep that judged those strings
// against the wrong server's pane set nulled 59 of 61 live bindings on
// 2026-08-05. Pairing the pane with the server that issued it makes both
// failures structurally impossible instead of guarded against.
//
// This change is IDENTITY ONLY. It does not decide liveness; liveness moved out
// of the database entirely (internal/monitor/liveness.go derives it per
// invocation by JOINing a snapshot). That is why this change also DROPS
// E-1530's two `sessions_null_process_on_end_*` triggers rather than porting
// them: they erase a binding at end-of-life, which is a destructive write driven
// by no user action, and it destroys the very history that diagnosed the
// incident.
//
// BACKFILL AND ITS LIMIT. Existing bindings get a processes row with
// server_uuid NULL, because which tmux server issued a pane months ago is not
// knowable retroactively. Those rows read 'unknown' — never 'dead' — so nothing
// is condemned, and they self-heal to a real server_uuid the next time the
// session fires a hook (seconds, in practice). A row whose `process` matches
// neither "%N" nor "pid:N" is left with process_id NULL rather than guessed at:
// a wrong guess mints an identity that can never match an observation, which is
// worse than an honest absence.
//
// Authored as a .go change (not .sql) for the same reason as e-1571: the runner
// opens its own connection with foreign_keys at SQLite's default (OFF), so
// `ALTER TABLE ... ADD COLUMN ... REFERENCES` is permitted. The //go:build
// ignore tag keeps this one-off `package main` program out of `go build/vet/test
// ./...`; `go run <path>`, which the apply-change dispatcher uses, still runs it.
//
// Runs once, at land time, against the populated real DB. The sandbox and tests
// build from schema.sql, which declares the post-migration shape directly.
package main

import (
	"database/sql"

	"github.com/mikeschinkel/endless/internal/schema/changes/runner"
)

// legacyTmux / legacyPID classify a pre-E-1898 `sessions.process` string by
// shape. Kept as SQL fragments rather than a Go pass over the rows so the whole
// migration stays one set of statements in one transaction.
const (
	legacyPID  = `substr(process, 1, 4) = 'pid:'`
	legacyTmux = `substr(process, 1, 1) = '%'`
	// Anything matching neither shape is not a binding we can honestly
	// reconstruct; every statement below is scoped by this predicate.
	legacyBound = `process IS NOT NULL AND process != '' AND (` +
		legacyTmux + ` OR ` + legacyPID + `)`
	// kind_id / address derivations, shared by the INSERT and the UPDATE so the
	// two cannot disagree about what a given string means.
	legacyKind    = `CASE WHEN ` + legacyPID + ` THEN 2 ELSE 1 END`
	legacyAddress = `CASE WHEN ` + legacyPID + ` THEN substr(process, 5) ELSE process END`
)

func main() {
	runner.Run(func(tx *sql.Tx) error {
		stmts := []string{
			// ── the enum mirror ────────────────────────────────────────────
			`CREATE TABLE IF NOT EXISTS process_kinds (
				id    INTEGER PRIMARY KEY,
				slug  TEXT UNIQUE NOT NULL,
				label TEXT NOT NULL
			)`,
			`INSERT INTO process_kinds (id, slug, label) VALUES
				(1, 'tmux', 'Tmux pane'),
				(2, 'pid',  'OS process')
			 ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label`,

			// ── durable identity ───────────────────────────────────────────
			`CREATE TABLE IF NOT EXISTS processes (
				id            INTEGER PRIMARY KEY,
				kind_id       INTEGER NOT NULL DEFAULT 1,
				server_uuid   TEXT,
				address       TEXT NOT NULL,
				first_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
				last_seen_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
				FOREIGN KEY (kind_id) REFERENCES process_kinds(id)
			)`,
			// ifnull() rather than a plain UNIQUE: SQLite treats each NULL as
			// distinct, so a plain constraint would not bind for kind=pid or for
			// the NULL-server backfill below — precisely the rows this migration
			// creates. Must exist BEFORE the INSERT, which relies on OR IGNORE.
			`CREATE UNIQUE INDEX IF NOT EXISTS processes_identity
				ON processes (kind_id, ifnull(server_uuid, ''), address)`,

			// ── the new pointer ────────────────────────────────────────────
			// REFERENCES is only legal here because the runner's connection has
			// foreign_keys OFF (see file header). The FK persists in the table
			// schema and is enforced once the app reopens with it ON.
			`ALTER TABLE sessions ADD COLUMN process_id INTEGER REFERENCES processes(id)`,

			// ── backfill ───────────────────────────────────────────────────
			// One processes row per distinct legacy binding, server unknown.
			`INSERT OR IGNORE INTO processes (kind_id, server_uuid, address)
			 SELECT DISTINCT ` + legacyKind + `, NULL, ` + legacyAddress + `
			 FROM sessions WHERE ` + legacyBound,

			`UPDATE sessions SET process_id = (
				SELECT p.id FROM processes p
				WHERE p.kind_id = ` + legacyKind + `
				  AND p.server_uuid IS NULL
				  AND p.address = ` + legacyAddress + `
			 ) WHERE ` + legacyBound,

			// ── remove the destructive backstop ────────────────────────────
			// Dropped, not ported: see the file header. Must precede the column
			// drop regardless — SQLite refuses DROP COLUMN while a trigger
			// references the column.
			`DROP TRIGGER IF EXISTS sessions_null_process_on_end_update`,
			`DROP TRIGGER IF EXISTS sessions_null_process_on_end_insert`,

			`ALTER TABLE sessions DROP COLUMN process`,
		}
		for _, s := range stmts {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		return nil
	})
}
