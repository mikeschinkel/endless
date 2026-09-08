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

// TestSchema_AppliesToDBPredatingErrorsProjectID is the E-1960 half of the same
// ordering constraint, and it pins the trick that made it survivable.
//
// E-1960 both ADDS errors.project_id and REDEFINES the partial unique index over
// it. The index cannot be renamed: a new name would make CREATE UNIQUE INDEX
// IF NOT EXISTS actually run against the pre-migration DB, resolve project_id
// eagerly, and abort schema application — the deadlock the test above describes.
// Keeping E-698's name means IF NOT EXISTS short-circuits on the OLD index, and
// the change file swaps the definition afterwards.
//
// So this test asserts two things a reader would not otherwise be able to trust:
// schema.SQL survives the pre-migration DB, and it leaves the old index exactly
// as it found it rather than quietly replacing it.
func TestSchema_AppliesToDBPredatingErrorsProjectID(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema to a fresh DB: %v", err)
	}

	// Roll `errors` back to its pre-E-1960 shape: E-698's index over
	// (source, code, fingerprint), and no project_id at all. The index goes
	// first — SQLite will not drop a column an index still names.
	mustExec(t, db, `DROP INDEX IF EXISTS idx_errors_open_uniq`)
	mustExec(t, db, `ALTER TABLE errors DROP COLUMN project_id`)
	mustExec(t, db, `CREATE UNIQUE INDEX idx_errors_open_uniq
	                     ON errors(source, code, fingerprint) WHERE cleared_at IS NULL`)

	// The connect `apply-change` performs before running the change script.
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema to a DB predating errors.project_id: %v\n"+
			"the open-incident index must keep E-698's NAME so IF NOT EXISTS "+
			"short-circuits here — see the note above it in schema.sql", err)
	}

	// Short-circuited, not replaced: the pre-migration definition is still the
	// one in the DB. If this ever reads project_id, schema.SQL has started doing
	// the migration's job and the assertion above stopped meaning anything.
	var indexSQL string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_errors_open_uniq'`,
	).Scan(&indexSQL); err != nil {
		t.Fatalf("read the open-incident index: %v", err)
	}
	if strings.Contains(indexSQL, "project_id") {
		t.Errorf("schema.SQL rewrote the open-incident index on a pre-migration DB: %s", indexSQL)
	}

	// And once the change script runs, the index qualifies by project. These two
	// statements MIRROR internal/schema/changes/e-1960-add-errors-project-id.go;
	// the change file itself is executed end-to-end by E-1960's verify suite,
	// which can give it a real database file to open.
	mustExec(t, db, `ALTER TABLE errors ADD COLUMN project_id INTEGER
	                     REFERENCES projects(id) ON DELETE SET NULL`)
	mustExec(t, db, `DROP INDEX IF EXISTS idx_errors_open_uniq`)
	mustExec(t, db, `CREATE UNIQUE INDEX idx_errors_open_uniq
	                     ON errors(COALESCE(project_id, 0), source, code, fingerprint)
	                  WHERE cleared_at IS NULL`)

	// Two projects, one fingerprint, two rows. That is the whole point of the
	// migration: before it, the second insert collided with the first.
	mustExec(t, db, `INSERT INTO projects (id, name, path) VALUES
	                     (1, 'alpha', '~/Projects/alpha'), (2, 'beta', '~/Projects/beta')`)
	for _, pid := range []int{1, 2} {
		if _, err := db.Exec(
			`INSERT INTO errors (project_id, code, severity, source, fingerprint, summary)
			 VALUES (?, 'ERR-0001', 'error', 's', 'f', 'same everywhere')`, pid,
		); err != nil {
			t.Fatalf("insert for project %d: %v", pid, err)
		}
	}
	assertCount(t, db, "errors", 2)
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
