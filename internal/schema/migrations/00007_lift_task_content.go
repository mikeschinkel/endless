package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

// liftTaskContent is migration 7 (E-1531): move the values of tasks.plan,
// tasks.outcome, tasks.analysis and tasks.notes into task_content rows (00006
// added the table), then drop the four columns. tasks.description is NOT one of
// them — it is short metadata and stays a column.
//
// WHICH NAME EACH VALUE TAKES
//
// plan, analysis and notes carry over under their own names. outcome is split,
// because the column carried two unrelated things:
//
//   - on a task whose status is declined, obsolete or superseded, the value was
//     written as the reason the task ended — the abandonment guard demanded it
//     at that transition — so it becomes `reason`;
//   - everything else becomes `outcome`: a research or brainstorm task's
//     findings, and the note a todo or bugfix was confirmed or assumed with.
//
// Status decides, not type. A research task that was later abandoned had its
// findings overwritten by the closing reason (every abandonment wrote the column
// the findings lived in), so what its row holds is a reason. The five such rows
// on the development database when this was written all read as one
// ("Superseded by …", "Declined: …"), which is what settled the precedence. The
// status list is written out rather than read from internal/taskstatus: a
// migration is frozen at the moment it was written, and a group edited later
// must not change what this step did.
//
// Only non-empty values become rows. An empty or NULL column is "no content",
// and task_content has no empty rows — a row's presence means the task has that
// content.
//
// WHY THIS STEP IS `.go`
//
// For the reason 00003 gives: SQLite has no DROP COLUMN IF EXISTS, and a
// database schema.sql built reaches this step already lacking the columns. Each
// column is probed, and one that is absent is neither copied nor dropped. The
// copy has to live here rather than in 00006 for the same reason — an INSERT ...
// SELECT naming an absent column is a hard error, and SQL has no way to ask.
//
// THE NOTICE TRIGGERS ARE SET ASIDE FOR THE COPY
//
// 00006 installed task_content_notify_*, which fire on INSERT. Left in place,
// lifting every existing plan, analysis and notes value would tell every live
// session that each task it holds just gained them — hundreds of false notices
// on the first connect after upgrading. So the step reads the three triggers'
// DDL out of sqlite_master, drops them, copies, and re-executes the DDL it read.
// Re-executing what was read rather than a copy typed here keeps the triggers
// byte-identical to 00006 and schema.sql, which TestMigrate_MatchesSchemaSQL
// asserts, and keeps this file from carrying a third copy of them.
//
// RunTx, so goose's transaction wraps the whole step: the copy, the trigger
// round-trip and the drops land together or not at all.
//
// ORDERING, the hazard 00003 documents: once this applies, an endless-go binary
// older than it still names the columns and fails with "no such column". `just
// land` rebuilds the binaries immediately after main advances, so the window is
// inside one land.
//
// No Down: the values live in task_content now, and a Down that re-created
// empty columns would restore the shape without the data.
func liftTaskContent() *goose.Migration {
	return goose.NewGoMigration(7, &goose.GoFunc{RunTx: liftTaskContentUp}, nil)
}

// liftedColumns are the four content columns, in the order they are copied.
var liftedColumns = []string{"plan", "outcome", "analysis", "notes"}

// abandonedAtLift are the statuses whose stored outcome is a closing reason.
// Frozen here; see liftTaskContent.
const abandonedAtLift = `'declined', 'obsolete', 'superseded'`

func liftTaskContentUp(ctx context.Context, tx *sql.Tx) error {
	var present []string
	for _, col := range liftedColumns {
		ok, err := columnExists(ctx, tx, "tasks", col)
		if err != nil {
			return err
		}
		if ok {
			present = append(present, col)
		}
	}
	if len(present) == 0 {
		return nil
	}

	triggers, err := setAsideContentTriggers(ctx, tx)
	if err != nil {
		return err
	}
	for _, col := range present {
		name := `'` + col + `'`
		if col == "outcome" {
			name = `CASE WHEN status IN (` + abandonedAtLift + `) THEN 'reason' ELSE 'outcome' END`
		}
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO task_content (task_id, name, content)
			 SELECT id, `+name+`, "`+col+`" FROM tasks
			  WHERE COALESCE("`+col+`", '') != ''`,
		); err != nil {
			return fmt.Errorf("lifting tasks.%s into task_content: %w", col, err)
		}
	}
	for _, ddl := range triggers {
		if _, err = tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("restoring a task_content trigger: %w", err)
		}
	}
	for _, col := range present {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE tasks DROP COLUMN "`+col+`"`); err != nil {
			return fmt.Errorf("dropping tasks.%s: %w", col, err)
		}
	}
	return nil
}

// setAsideContentTriggers drops the task_content triggers and returns their DDL,
// exactly as sqlite_master stored it, for re-execution once the copy is done.
func setAsideContentTriggers(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT name, sql FROM sqlite_master
		  WHERE type = 'trigger' AND tbl_name = 'task_content'
		  ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("reading task_content triggers: %w", err)
	}
	type trigger struct{ name, ddl string }
	var found []trigger
	for rows.Next() {
		var t trigger
		if err = rows.Scan(&t.name, &t.ddl); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("reading task_content triggers: %w", err)
		}
		found = append(found, t)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	ddls := make([]string, 0, len(found))
	for _, t := range found {
		if _, err = tx.ExecContext(ctx, `DROP TRIGGER "`+t.name+`"`); err != nil {
			return nil, fmt.Errorf("setting aside trigger %s: %w", t.name, err)
		}
		ddls = append(ddls, t.ddl)
	}
	return ddls, nil
}
