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
