package schema_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/gatekind"
	"github.com/mikeschinkel/endless/internal/processkind"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// TestMigrate_MatchesSchemaSQL is the assertion that the baseline is honest: a
// database goose builds and a database schema.sql builds are the same database.
//
// It runs against the LIVE schema.sql, not a pinned copy, and that is the point
// while both still exist. schema.sql is still committed and still the artifact a
// human reads (E-2021 makes it generated); until then the two can drift in
// either direction, and this fails on both — a schema.sql edited without a
// migration, and a migration added without updating schema.sql.
//
// E-2019's own verify suite makes the other, narrower comparison: the baseline
// alone against a copy of schema.sql pinned at land time. That one can never
// move. This one is supposed to.
func TestMigrate_MatchesSchemaSQL(t *testing.T) {
	migrated := shapeOf(t, buildDB(t, func(db *sql.DB) error {
		return schema.Migrate(db)
	}))
	declared := shapeOf(t, buildDB(t, func(db *sql.DB) error {
		_, err := db.Exec(schema.SQL)
		return err
	}))

	if migrated == declared {
		return
	}
	t.Errorf("a migrated database and a schema.sql database differ.\n"+
		"Neither file is allowed to move without the other: add a migration for a\n"+
		"schema.sql edit, and mirror a migration back into schema.sql until E-2021\n"+
		"generates it.\n%s", firstDifference(migrated, declared))
}

// TestMigrate_SeedsSurviveTheReplay proves a freshly migrated database passes
// every enum mirror gate monitor.DB() runs on connect. Before E-2019 these rows
// came from schema.sql on every connect; they now come from seeds.sql, and a
// database that reached the right SHAPE with no enum ROWS would fail closed on
// its very first connect with nothing to point at.
func TestMigrate_SeedsSurviveTheReplay(t *testing.T) {
	db := buildDB(t, func(db *sql.DB) error { return schema.Migrate(db) })

	if err := tasktype.VerifyIntegrity(db); err != nil {
		t.Errorf("task_types: %v", err)
	}
	if err := processkind.VerifyIntegrity(db); err != nil {
		t.Errorf("process_kinds: %v", err)
	}
	if err := gatekind.VerifyIntegrity(db); err != nil {
		t.Errorf("gate_kinds: %v", err)
	}
	if err := sessiontaskrelation.VerifyIntegrity(db); err != nil {
		t.Errorf("session_task_relations: %v", err)
	}
}

// TestMigrate_SeedsReconcileOnEveryRun pins E-1659's self-heal across the move
// to goose. schema.sql's upsert seeds ran on every connect and repaired an enum
// row left behind by a rename; a migration runs once and could not. seeds.sql
// is applied per Migrate call precisely so that behaviour did not quietly go
// away with the mechanism that used to provide it.
//
// This is the goose-side twin of TestSchema_ReconcilesRenamedEnumRows.
func TestMigrate_SeedsReconcileOnEveryRun(t *testing.T) {
	db := buildDB(t, func(db *sql.DB) error { return schema.Migrate(db) })

	mustExec(t, db, `UPDATE task_types SET slug='task', label='Task' WHERE id=1`)
	mustExec(t, db, `UPDATE process_kinds SET slug='stale', label='Stale' WHERE id=1`)

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	assertRow(t, db, "task_types", 1, "todo", "Todo")
	assertRow(t, db, "process_kinds", 1, "tmux", "Tmux pane")
	assertCount(t, db, "task_types", 5)
	assertCount(t, db, "process_kinds", 2)
}

// TestMigrate_IsIdempotent covers the call pattern monitor.DB() actually has:
// every connect, forever. The second run must be a no-op and must not fail.
func TestMigrate_IsIdempotent(t *testing.T) {
	db := buildDB(t, func(db *sql.DB) error { return schema.Migrate(db) })
	before := shapeOf(t, db)

	for i := range 3 {
		if err := schema.Migrate(db); err != nil {
			t.Fatalf("migrate run %d: %v", i+2, err)
		}
	}
	if after := shapeOf(t, db); after != before {
		t.Errorf("re-migrating changed the schema:\n%s", firstDifference(before, after))
	}
	// One bookkeeping row per applied migration, plus goose's own version-0 row.
	// Derived rather than pinned at a literal: every migration added to the set
	// bumps this, and a test that had to be re-typed for that would be a test
	// that says nothing about idempotence.
	latest, err := schema.LatestVersion()
	if err != nil {
		t.Fatalf("latest version: %v", err)
	}
	assertCount(t, db, "goose_db_version", int(latest)+1)
}

// TestMigrate_LeavesAPreVersioningDatabaseIntact is the assertion the real
// ledger depends on, reproduced on a database built exactly the way it was.
//
// There is one database in the world that predates versioning — the ledger at
// ~/.config/endless/endless.db — and it acquired its shape by exec'ing
// schema.sql on connect, with nothing recording a version anywhere. Its first
// connect after E-2019 replays the baseline. That replay must be inert: it must
// create nothing, destroy nothing, and leave the rows alone.
//
// It ends at the LATEST version, not at the baseline. That was the same number
// while the baseline was the whole set; E-2161 added the second migration and
// separated them. The fixture is built from the CURRENT schema.sql, so every
// later migration meets a database that already declares what it creates and is
// likewise inert — which is the property under test, and the reason the shape
// comparison below is the assertion that matters rather than the version.
//
// Run for real once during E-2019 against a 127MB VACUUM INTO snapshot of the
// live ledger (1,448 tasks, 1,199 sessions): shape identical, row counts
// identical, 40ms, version 1. This test is that proof in a form the suite can
// keep re-running, since a durable test cannot reach for the real database.
func TestMigrate_LeavesAPreVersioningDatabaseIntact(t *testing.T) {
	// Exactly how the real ledger came to be.
	db := buildDB(t, func(db *sql.DB) error {
		_, err := db.Exec(schema.SQL)
		return err
	})
	if versioned, _ := tableExists(db, "goose_db_version"); versioned {
		t.Fatal("a schema.sql database should carry no goose version table")
	}
	mustExec(t, db, `INSERT INTO projects (id, name, path) VALUES (1, 'alpha', '~/a')`)
	mustExec(t, db, `INSERT INTO tasks (id, project_id, title) VALUES (7, 1, 'survives')`)
	before := shapeOf(t, db)

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrating a pre-versioning database: %v", err)
	}

	if after := shapeOf(t, db); after != before {
		t.Errorf("the replay changed the schema of a database that already had it:\n%s",
			firstDifference(before, after))
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM tasks WHERE id = 7`).Scan(&title); err != nil {
		t.Fatalf("the pre-existing row did not survive: %v", err)
	}
	version, err := schema.DBVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	latest, err := schema.LatestVersion()
	if err != nil {
		t.Fatalf("latest version: %v", err)
	}
	if version != latest {
		t.Errorf("recorded version = %d, want the latest %d", version, latest)
	}
}

// TestMigrate_FreshDatabaseReachesLatest pins the other half: an empty database
// ends at the newest version in the set, not merely at the baseline. It is the
// test that starts failing the day a migration is added and a code path forgets
// to run it.
func TestMigrate_FreshDatabaseReachesLatest(t *testing.T) {
	db := buildDB(t, func(db *sql.DB) error { return schema.Migrate(db) })

	latest, err := schema.LatestVersion()
	if err != nil {
		t.Fatalf("latest version: %v", err)
	}
	version, err := schema.DBVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != latest {
		t.Errorf("fresh database at version %d, want the latest %d", version, latest)
	}
	if latest < schema.BaselineVersion {
		t.Errorf("the migration set is empty: latest = %d", latest)
	}
}

// buildDB opens a file-backed database and runs build against it. File-backed
// rather than :memory: because the schema carries an FTS5 virtual table, and
// because this is the storage every caller of Migrate actually uses.
func buildDB(t *testing.T, build func(*sql.DB) error) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "endless.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err = build(db); err != nil {
		t.Fatalf("build database: %v", err)
	}
	return db
}

// shapeOf renders a database's schema as one comparable string: every object it
// declares, whitespace-collapsed so formatting differences do not read as
// schema differences. SQLite's own objects and the version bookkeeping of
// whichever mechanism built it are excluded — they are not the schema.
func shapeOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`
		SELECT type, name, COALESCE(sql, '')
		  FROM sqlite_master
		 WHERE name NOT LIKE 'sqlite_%'
		   AND name <> 'goose_db_version'
		 ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var kind, name, ddl string
		if err = rows.Scan(&kind, &name, &ddl); err != nil {
			t.Fatalf("scan sqlite_master: %v", err)
		}
		out = append(out, kind+" "+name+": "+strings.Join(strings.Fields(ddl), " "))
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	return strings.Join(out, "\n")
}

// firstDifference names the first line the two shapes disagree on, so a failure
// points at one object rather than printing two 60-object dumps.
func firstDifference(a, b string) string {
	aLines, bLines := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(aLines), len(bLines)) {
		var left, right string
		if i < len(aLines) {
			left = aLines[i]
		}
		if i < len(bLines) {
			right = bLines[i]
		}
		if left != right {
			return "  migrated: " + orNone(left) + "\n  schema.sql: " + orNone(right)
		}
	}
	return "  (no line differs; the difference is in ordering)"
}

func orNone(s string) string {
	if s == "" {
		return "(absent)"
	}
	return s
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n)
	return n > 0, err
}

// TestMigrate_LiftsTaskContent drives 00007 over a database shaped as it was
// before E-1531: the four content columns populated on tasks, sessions holding
// those tasks. After the step, every value is a task_content row under the
// right name, the columns are gone, and not one notice was written.
func TestMigrate_LiftsTaskContent(t *testing.T) {
	db := buildDB(t, func(db *sql.DB) error { return schema.MigrateUpTo(db, 5) })

	mustExec(t, db, `INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')`)
	for _, row := range []struct {
		id                             int
		status                         string
		plan, outcome, analysis, notes any
	}{
		{10, "completed", "the plan", "the findings", "the analysis", "the notes"},
		{11, "declined", nil, "why it was declined", nil, ""},
		{12, "obsolete", "", "why it no longer applies", nil, nil},
		{13, "superseded", nil, "why it was handed on", nil, nil},
		{14, "confirmed", nil, "verified by hand", nil, nil},
		{15, "ready", nil, "", nil, nil},
	} {
		mustExec(t, db,
			`INSERT INTO tasks (id, project_id, title, status, plan, outcome, analysis, notes)
			 VALUES (?, 1, 't', ?, ?, ?, ?, ?)`,
			row.id, row.status, row.plan, row.outcome, row.analysis, row.notes)
	}
	// A live session holding task 10: were the content triggers left armed for
	// the copy, it would be told the task just gained a plan, analysis and notes.
	mustExec(t, db, `INSERT INTO sessions (id, project_id, state) VALUES (900, 1, 'working')`)
	mustExec(t, db, `INSERT INTO session_tasks (session_id, task_id, created_at, updated_at) VALUES (900, 10, '2026-01-01', '2026-01-01')`)

	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := db.Query(`SELECT task_id, name, content FROM task_content ORDER BY task_id, name`)
	if err != nil {
		t.Fatalf("read task_content: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id int
		var name, content string
		if err = rows.Scan(&id, &name, &content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, fmt.Sprintf("%d %s=%s", id, name, content))
	}
	want := []string{
		"10 analysis=the analysis",
		"10 notes=the notes",
		"10 outcome=the findings",
		"10 plan=the plan",
		"11 reason=why it was declined",
		"12 reason=why it no longer applies",
		"13 reason=why it was handed on",
		"14 outcome=verified by hand",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("task_content after the lift:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	for _, col := range []string{"plan", "outcome", "analysis", "notes"} {
		var n int
		if err = db.QueryRow(
			`SELECT count(*) FROM pragma_table_info('tasks') WHERE name = ?`, col,
		).Scan(&n); err != nil {
			t.Fatalf("probe %s: %v", col, err)
		}
		if n != 0 {
			t.Errorf("tasks.%s survived the migration", col)
		}
	}

	var notices int
	if err = db.QueryRow(`SELECT count(*) FROM session_notices`).Scan(&notices); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if notices != 0 {
		t.Errorf("the lift wrote %d session notices; it must write none", notices)
	}

	// The triggers came back: a real edit after the migration still notifies.
	mustExec(t, db, `UPDATE task_content SET content = 'revised' WHERE task_id = 10 AND name = 'plan'`)
	if err = db.QueryRow(`SELECT count(*) FROM session_notices`).Scan(&notices); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if notices != 1 {
		t.Errorf("a plan edit after the lift wrote %d notices, want 1", notices)
	}
}
