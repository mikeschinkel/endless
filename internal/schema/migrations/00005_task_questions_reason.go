// E-2176: add task_questions.reason.
//
// Why a question was closed without an answer — withdrawn, rejected as invalid,
// or superseded. Its own column rather than text stored in `answer`: a question
// closed that way has no answer, and one column holding two different facts is
// the overloading tasks.outcome carries today. Exactly one of the two is set on
// a closed question, except that a superseded question keeps the answer it had.
// Required on every closing move; the requirement is enforced in
// internal/events/question.go, since schema.sql carries no CHECK constraints.
//
// A .go step for the reason the package header gives: schema.sql is still
// exec'd directly and already declares the column, so a database can reach this
// step holding it, and SQLite has no ADD COLUMN IF NOT EXISTS. It probes the one
// column and skips only that.
//
// SQLite places an added column after the table's last column and before its
// table constraints; schema.sql declares `reason` in that same position, which
// is what keeps TestMigrate_MatchesSchemaSQL comparing like with like.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func addTaskQuestionsReason() *goose.Migration {
	return goose.NewGoMigration(5, &goose.GoFunc{RunTx: addTaskQuestionsReasonUp}, nil)
}

func addTaskQuestionsReasonUp(ctx context.Context, tx *sql.Tx) error {
	present, err := columnExists(ctx, tx, "task_questions", "reason")
	if err != nil || present {
		return err
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE task_questions ADD COLUMN reason TEXT`); err != nil {
		return fmt.Errorf("adding task_questions.reason: %w", err)
	}
	return nil
}
