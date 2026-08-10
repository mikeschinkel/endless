package schema_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// TestSchema_ReconcilesRenamedEnumRows proves the E-1659 self-heal: the enum
// mirror seeds (task_types, session_kinds) use INSERT ... ON CONFLICT DO UPDATE,
// so re-applying schema.SQL to a populated DB whose rows were seeded under an
// OLD slug/label reconciles them to the current values — with no change-file.
// schema.SQL runs on every monitor.DB() connect (before VerifyIntegrity), so a
// rename self-heals on the next connect. INSERT OR IGNORE could not do this: it
// would skip the existing id and leave the stale slug, tripping VerifyIntegrity.
func TestSchema_ReconcilesRenamedEnumRows(t *testing.T) {
	// A single connection keeps the :memory: DB alive across both Exec calls.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// First connect: creates tables and seeds the current values.
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema (1st): %v", err)
	}

	// Simulate a DB populated under the pre-rename names / a drifted mirror.
	mustExec(t, db, `UPDATE task_types SET slug='task', label='Task' WHERE id=1`)
	mustExec(t, db, `UPDATE task_types SET slug='bug',  label='Bug'  WHERE id=2`)
	mustExec(t, db, `UPDATE session_kinds SET slug='stale', label='Stale' WHERE id=1`)

	// Second connect: the upsert seeds must reconcile the stale rows.
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema (2nd): %v", err)
	}

	assertRow(t, db, "task_types", 1, "todo", "Todo")
	assertRow(t, db, "task_types", 2, "bugfix", "Bugfix")
	assertRow(t, db, "session_kinds", 1, "tmux", "Tmux")

	// The reconcile must UPDATE in place, never insert duplicates.
	assertCount(t, db, "task_types", 5)
	assertCount(t, db, "session_kinds", 2)
}

// TestSchema_AppliesToDBPredatingItsNewestColumn pins the ordering constraint
// that broke E-1929's first land attempt.
//
// schema.SQL runs on EVERY monitor.DB() connect — including the one
// `endless db apply-change` opens before it dispatches to the change script that
// adds a new column. So the NEW schema.SQL always meets the OLD, column-less DB
// first, and it must survive that meeting or the migration can never run: the
// change deadlocks its own rollout, and the land aborts with
// "no such column: <the column the change was about to add>".
//
// The trap is that CREATE VIEW and CREATE INDEX differ here. A view body is
// resolved at PREPARE time, so a view over a not-yet-existing column creates
// fine. An index resolves its columns at CREATE time and fails immediately. The
// same goes for any other eagerly-resolved reference (generated column, CHECK).
//
// This test simulates a populated pre-migration DB by stripping `tasks.removed`
// back off a fresh one, then re-applies schema.SQL and requires it to succeed.
// It is deliberately written against `removed` — the concrete column that caused
// the failure — rather than in the abstract, so it fails loudly if someone later
// adds an eager reference to it.
func TestSchema_AppliesToDBPredatingItsNewestColumn(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema to a fresh DB: %v", err)
	}

	// Roll `tasks` back to its pre-E-1929 shape. Every schema object that could
	// name the column has to go first — SQLite refuses to drop a column while one
	// still references it — and they are dropped GENERICALLY rather than by name
	// so that a future eager reference is stripped here and then fails on the
	// re-apply below, which is where production fails, rather than tripping this
	// teardown with a different error.
	for _, q := range dropStmts(t, db,
		`SELECT type, name FROM sqlite_master
		  WHERE sql IS NOT NULL
		    AND (type = 'view' OR (type = 'index' AND tbl_name = 'tasks'))`,
	) {
		mustExec(t, db, q)
	}
	mustExec(t, db, `ALTER TABLE tasks DROP COLUMN removed`)

	// The connect that `apply-change` performs before running the change script.
	// Anything in schema.SQL that resolves `removed` eagerly dies right here.
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema to a DB predating tasks.removed: %v\n"+
			"schema.SQL must survive the connect that precedes its own migration — "+
			"see the CREATE INDEX note above the live_tasks view", err)
	}

	// The view is recreated and is still just a declaration at this point: it
	// names a column the table does not have, and that is legal until queried.
	var views int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='view' AND name='live_tasks'`,
	).Scan(&views); err != nil {
		t.Fatalf("count live_tasks view: %v", err)
	}
	if views != 1 {
		t.Errorf("live_tasks view present = %d, want 1", views)
	}

	// And once the change script adds the column, the view resolves.
	mustExec(t, db, `ALTER TABLE tasks ADD COLUMN removed INTEGER NOT NULL DEFAULT 0`)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM live_tasks`).Scan(&n); err != nil {
		t.Fatalf("query live_tasks after the column lands: %v", err)
	}
}

// dropStmts turns a (type, name) query over sqlite_master into the DROP
// statements for what it matched.
func dropStmts(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("enumerate schema objects: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			t.Fatalf("scan schema object: %v", err)
		}
		out = append(out, "DROP "+kind+" "+name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("enumerate schema objects: %v", err)
	}
	return out
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func assertRow(t *testing.T, db *sql.DB, table string, id int, wantSlug, wantLabel string) {
	t.Helper()
	var slug, label string
	err := db.QueryRow("SELECT slug, label FROM "+table+" WHERE id = ?", id).Scan(&slug, &label)
	if err != nil {
		t.Fatalf("%s id=%d: %v", table, id, err)
	}
	if slug != wantSlug || label != wantLabel {
		t.Errorf("%s id=%d = (%q,%q), want (%q,%q)", table, id, slug, label, wantSlug, wantLabel)
	}
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if n != want {
		t.Errorf("%s row count = %d, want %d", table, n, want)
	}
}
