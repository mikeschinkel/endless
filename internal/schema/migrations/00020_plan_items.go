// E-2280: itemized plans and their reconciliation.
//
//   - task_plan_items, task_reconciliations, task_reconciliation_entries — the
//     rationale lives beside them in internal/schema/schema.sql, and the DDL
//     below must stay byte-comparable with it (TestMigrate_MatchesSchemaSQL).
//   - task_questions.kind — the discrepancy kind a reconciliation files a
//     question under; NULL on an ordinary question.
//
// A .go step for 00005's reason: schema.sql already declares the column, and
// SQLite has no ADD COLUMN IF NOT EXISTS, so it is probed. The tables use IF NOT
// EXISTS for the same reason.
//
// No Down: goose never rolls this set back in production.
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
)

func planItems() *goose.Migration {
	return goose.NewGoMigration(20, &goose.GoFunc{RunTx: planItemsUp}, nil)
}

var planItemsTables = []string{
	`CREATE TABLE IF NOT EXISTS task_plan_items (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL,
    seq INTEGER NOT NULL,
    sort_order INTEGER NOT NULL,
    kind TEXT NOT NULL,
    statement TEXT NOT NULL,
    verify TEXT,
    detail TEXT,
    origin TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'active',
    approved_statement TEXT,
    approved_kind TEXT,
    approved_at TEXT,
    created_by_session INTEGER,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (task_id, seq),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
)`,
	`CREATE TABLE IF NOT EXISTS task_reconciliations (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL,
    seq INTEGER NOT NULL,
    fingerprint TEXT NOT NULL,
    plan_digest TEXT NOT NULL,
    head TEXT,
    plan_changed INTEGER NOT NULL DEFAULT 0,
    session_id INTEGER,
    created_at TEXT NOT NULL,
    UNIQUE (task_id, seq),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
)`,
	`CREATE TABLE IF NOT EXISTS task_reconciliation_entries (
    reconciliation_id INTEGER NOT NULL,
    item_seq INTEGER NOT NULL,
    verdict TEXT NOT NULL,
    note TEXT,
    question_id INTEGER,
    holds_reason TEXT,
    PRIMARY KEY (reconciliation_id, item_seq),
    FOREIGN KEY (reconciliation_id) REFERENCES task_reconciliations(id) ON DELETE CASCADE
)`,
}

func planItemsUp(ctx context.Context, tx *sql.Tx) error {
	present, err := columnExists(ctx, tx, "task_questions", "kind")
	if err != nil {
		return err
	}
	if !present {
		if _, err = tx.ExecContext(ctx, `ALTER TABLE task_questions ADD COLUMN kind TEXT`); err != nil {
			return fmt.Errorf("adding task_questions.kind: %w", err)
		}
	}
	for _, ddl := range planItemsTables {
		if _, err = tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("creating plan item tables: %w", err)
		}
	}
	return nil
}
