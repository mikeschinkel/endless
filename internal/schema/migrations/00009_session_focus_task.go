// E-2188: add sessions.focus_task_id.
//
// The task a session's conversation is on right now, as opposed to the one it
// claimed (sessions.task_id). A session is bound to one claimed task for its
// lifetime, but it files, plans and updates others along the way; this column
// records which of them it touched last, so `session status` and `session
// monitor` can highlight it. It is a display aid, not a binding — nothing
// gates on it, and an occasionally stale value is acceptable.
//
// Set by upsertSessionTask (internal/events/session_tasks.go) on every claimed,
// surfaced or revisited touch. ON DELETE SET NULL because a removed task simply
// stops being anyone's focus.
//
// A .go step for the reason the package header gives: schema.sql is still
// exec'd directly and already declares the column, so a database can reach this
// step holding it, and SQLite has no ADD COLUMN IF NOT EXISTS. It probes the one
// column and skips only that.
//
// SQLite splices an added column's text in after the table's last column and
// before its table constraints; schema.sql declares focus_task_id in that same
// position and spelling, which is what keeps TestMigrate_MatchesSchemaSQL
// comparing like with like.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func addSessionFocusTask() *goose.Migration {
	return goose.NewGoMigration(9, &goose.GoFunc{RunTx: addSessionFocusTaskUp}, nil)
}

func addSessionFocusTaskUp(ctx context.Context, tx *sql.Tx) error {
	present, err := columnExists(ctx, tx, "sessions", "focus_task_id")
	if err != nil || present {
		return err
	}
	if _, err = tx.ExecContext(ctx,
		`ALTER TABLE sessions ADD COLUMN focus_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL`,
	); err != nil {
		return fmt.Errorf("adding sessions.focus_task_id: %w", err)
	}
	return nil
}
