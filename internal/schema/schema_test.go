package schema_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// TestSchema_ReconcilesRenamedEnumRows proves the E-1659 self-heal: the enum
// mirror seeds (task_types, session_kinds) use INSERT ... ON CONFLICT DO UPDATE,
// so re-applying schema.SQL to a populated DB whose rows were seeded under an
// OLD slug/label reconciles them to the current values — with no migration of
// its own. The seeds run after every migration (before VerifyIntegrity), so a
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
	mustExec(t, db, `UPDATE process_kinds SET slug='stale', label='Stale' WHERE id=1`)

	// Second connect: the upsert seeds must reconcile the stale rows.
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema (2nd): %v", err)
	}

	assertRow(t, db, "task_types", 1, "todo", "Todo")
	assertRow(t, db, "task_types", 2, "bugfix", "Bugfix")
	assertRow(t, db, "process_kinds", 1, "tmux", "Tmux pane")

	// The reconcile must UPDATE in place, never insert duplicates.
	assertCount(t, db, "task_types", 5)
	assertCount(t, db, "process_kinds", 2)
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
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

// TestSchema_RebuildFuseIsStillDeclared pins the schema facts that
// `endless-go event rebuild-db --confirm` is refused over (E-2062), so a later
// sweep cannot quietly "fix" the refusal by removing what it is about.
//
// Two independent things are asserted:
//
//   - The fuse: sessions.task_id is write-once (ED-1560) and its FK still says
//     ON DELETE SET NULL. That pair is what aborts the command's DELETE today.
//     Relaxing either one is the step E-2062's "Sequencing" puts LAST, after
//     E-799 makes the copy-back whole — do it early and a loud refusal becomes
//     silent data loss.
//   - The cascade closure the refusal COUNTS. rebuild-db copies back only
//     tasks, decisions and decision_relations, so each edge below is a table
//     the DELETE empties and nothing refills. report_judgments and
//     report_labels hang off session_gates, not off tasks: they are reached on
//     the SECOND hop, which is why walking only the direct children of `tasks`
//     misses them.
//
// If any edge here changes, internal/eventcmd/rebuild_guard.go is describing a
// database that no longer exists and must be updated with it.
func TestSchema_RebuildFuseIsStillDeclared(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	var triggerSQL string
	err = db.QueryRow(
		`SELECT sql FROM sqlite_master
		  WHERE type = 'trigger' AND name = 'sessions_task_id_write_once'`,
	).Scan(&triggerSQL)
	if err != nil {
		t.Fatalf("sessions_task_id_write_once trigger is missing — it is the fuse "+
			"rebuild-db --confirm trips on (ED-1560, E-2062): %v", err)
	}
	if !strings.Contains(triggerSQL, "UPDATE OF task_id ON sessions") {
		t.Errorf("write-once trigger no longer fires on UPDATE OF task_id ON sessions:\n%s",
			triggerSQL)
	}

	edges := []struct {
		table    string
		column   string
		refTable string
		onDelete string
		why      string
	}{
		{"sessions", "task_id", "tasks", "SET NULL",
			"the implicit UPDATE that trips the write-once trigger"},
		{"task_landings", "task_id", "tasks", "CASCADE",
			"landing history, destroyed and never restored"},
		{"session_gates", "epic_id", "tasks", "CASCADE",
			"gates, destroyed and never restored"},
		{"report_judgments", "gate_id", "session_gates", "CASCADE",
			"second-hop loss, reached only through session_gates"},
		{"report_labels", "gate_id", "session_gates", "CASCADE",
			"second-hop loss, reached only through session_gates"},
	}

	for _, e := range edges {
		var onDelete string
		err := db.QueryRow(
			`SELECT "on_delete" FROM pragma_foreign_key_list(?)
			  WHERE "from" = ? AND "table" = ?`,
			e.table, e.column, e.refTable,
		).Scan(&onDelete)
		if err != nil {
			t.Errorf("%s.%s -> %s: FK is gone (%s): %v",
				e.table, e.column, e.refTable, e.why, err)
			continue
		}
		if onDelete != e.onDelete {
			t.Errorf("%s.%s -> %s ON DELETE %s, want %s (%s)",
				e.table, e.column, e.refTable, onDelete, e.onDelete, e.why)
		}
	}
}
