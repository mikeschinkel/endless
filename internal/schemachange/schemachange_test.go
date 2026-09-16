package schemachange_test

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/schemachange"
)

// openDB builds a throwaway database on disk. On disk rather than :memory:
// because Apply's .go path hands the path to a subprocess, and a test that
// could not exercise that path would leave half the applier unproven.
func openDB(t *testing.T) (db *sql.DB, dbPath dt.Filepath) {
	t.Helper()

	dbPath = dt.FilepathJoin(t.TempDir(), "endless.db")
	db, err := sql.Open("sqlite", string(dbPath))
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	t.Cleanup(func() {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("close %s: %v", dbPath, closeErr)
		}
	})
	db.SetMaxOpenConns(1)

	_, err = db.Exec("PRAGMA foreign_keys=ON")
	if err != nil {
		t.Fatalf("PRAGMA foreign_keys=ON: %v", err)
	}
	return db, dbPath
}

// writeChange puts a change file on disk under its real name, since the name IS
// the marker key and a fixture with a made-up name would test a key nothing uses.
func writeChange(t *testing.T, name, body string) dt.Filepath {
	t.Helper()

	path := dt.Filepath(filepath.Join(t.TempDir(), name))
	err := path.WriteFile([]byte(body), 0o644)
	if err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func markerCount(t *testing.T, db *sql.DB, name string) int {
	t.Helper()

	var count int
	err := db.QueryRow(
		"SELECT count(*) FROM _schema_version WHERE name = ?", name,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count markers for %q: %v", name, err)
	}
	return count
}

func TestName_IsTheBasenameWithoutTheExtension(t *testing.T) {
	tests := []struct {
		path dt.Filepath
		want string
	}{
		{"internal/schema/changes/e-2088-add-thing.sql", "e-2088-add-thing"},
		{"internal/schema/changes/e-2088-add-thing.go", "e-2088-add-thing"},
		{"/tmp/go-build123/b001/exe/e-2088-add-thing", "e-2088-add-thing"},
		{"e-2088.dotted.name.sql", "e-2088.dotted.name"},
	}

	for _, tt := range tests {
		got := schemachange.Name(tt.path)
		if got != tt.want {
			t.Errorf("Name(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// TestApply_SQLChangeRunsDDLAndItsDML is ED-1571's "in FULL" requirement as a
// test. A DDL-only apply would leave a migrated database unusable: schema.sql
// seeds the enum mirrors, and the fail-closed integrity gates read those rows on
// the next connect. So a change's INSERTs and backfills are as load-bearing as
// its CREATE, and both must commit with the marker.
func TestApply_SQLChangeRunsDDLAndItsDML(t *testing.T) {
	db, dbPath := openDB(t)

	_, err := db.Exec(`CREATE TABLE tasks (id INTEGER PRIMARY KEY, type_id INTEGER)`)
	if err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	_, err = db.Exec(`INSERT INTO tasks (id, type_id) VALUES (1, NULL), (2, NULL)`)
	if err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	path := writeChange(t, "e-2088-add-thing.sql", `
		CREATE TABLE task_types (
			id    INTEGER PRIMARY KEY,
			slug  TEXT NOT NULL,
			label TEXT NOT NULL
		);
		INSERT OR IGNORE INTO task_types (id, slug, label) VALUES
			(1, 'todo', 'Todo'),
			(2, 'bugfix', 'Bugfix');
		UPDATE tasks SET type_id = 1 WHERE type_id IS NULL;
	`)

	err = schemachange.EnsureVersionTable(db)
	if err != nil {
		t.Fatalf("EnsureVersionTable: %v", err)
	}
	if markerCount(t, db, "e-2088-add-thing") != 0 {
		t.Fatal("the change is recorded before it has run")
	}

	res, err := schemachange.Apply(db, dbPath, path, io.Discard)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Status != schemachange.StatusApplied {
		t.Errorf("status = %q, want %q", res.Status, schemachange.StatusApplied)
	}
	if res.Name != "e-2088-add-thing" {
		t.Errorf("name = %q, want %q", res.Name, "e-2088-add-thing")
	}

	// The DDL ran.
	var seeds int
	err = db.QueryRow("SELECT count(*) FROM task_types").Scan(&seeds)
	if err != nil {
		t.Fatalf("read task_types: %v", err)
	}
	// The DML ran: the seed rows the integrity gates need.
	if seeds != 2 {
		t.Errorf("task_types rows = %d, want 2 (the change's seed DML did not run)", seeds)
	}
	// And the backfill.
	var backfilled int
	err = db.QueryRow("SELECT count(*) FROM tasks WHERE type_id = 1").Scan(&backfilled)
	if err != nil {
		t.Fatalf("read tasks: %v", err)
	}
	if backfilled != 2 {
		t.Errorf("backfilled rows = %d, want 2", backfilled)
	}
	// The marker committed with them.
	if markerCount(t, db, "e-2088-add-thing") != 1 {
		t.Error("the change ran but was not recorded in _schema_version")
	}
}

// TestApply_CreatesTheVersionTableWhenAbsent covers the defensive creation.
// A database that has never had schema.sql exec'd against it still needs
// somewhere to record a marker, and ED-1571's executable never applies
// schema.sql — so if this did not hold, the very tool meant to migrate a bare
// database would be the one unable to.
func TestApply_CreatesTheVersionTableWhenAbsent(t *testing.T) {
	db, dbPath := openDB(t)

	var tables int
	err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='_schema_version'",
	).Scan(&tables)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	if tables != 0 {
		t.Fatal("the fixture already has a _schema_version table")
	}

	path := writeChange(t, "e-2088-bare.sql", "CREATE TABLE thing (id INTEGER);\n")
	_, err = schemachange.Apply(db, dbPath, path, io.Discard)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if markerCount(t, db, "e-2088-bare") != 1 {
		t.Error("the marker table was not created, or the marker not recorded")
	}
}

// TestApply_IsIdempotent is why a failed land is re-runnable: the retry applies
// only what is still outstanding, and says so rather than silently doing nothing.
func TestApply_IsIdempotent(t *testing.T) {
	db, dbPath := openDB(t)
	path := writeChange(t, "e-2088-once.sql", "CREATE TABLE thing (id INTEGER);\n")

	_, err := schemachange.Apply(db, dbPath, path, io.Discard)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	res, err := schemachange.Apply(db, dbPath, path, io.Discard)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if res.Status != schemachange.StatusSkipped {
		t.Errorf("status = %q, want %q", res.Status, schemachange.StatusSkipped)
	}
	if res.Reason != "already applied" {
		t.Errorf("reason = %q, want %q", res.Reason, "already applied")
	}
	if markerCount(t, db, "e-2088-once") != 1 {
		t.Error("re-applying duplicated the marker")
	}
}

// TestApply_FailureLeavesNothingBehind: the effects and the marker commit
// together or not at all. A change that half-applied and recorded itself would
// be unrecoverable by a re-run, which is the whole point of the marker.
func TestApply_FailureLeavesNothingBehind(t *testing.T) {
	db, dbPath := openDB(t)
	path := writeChange(t, "e-2088-broken.sql",
		"CREATE TABLE ok_so_far (id INTEGER);\nSELECT * FROM no_such_table;\n")

	_, err := schemachange.Apply(db, dbPath, path, io.Discard)
	if err == nil {
		t.Fatal("Apply succeeded on a change that references a missing table")
	}
	if !strings.Contains(err.Error(), "no_such_table") {
		t.Errorf("error does not name the cause: %v", err)
	}
	if markerCount(t, db, "e-2088-broken") != 0 {
		t.Error("a failed change recorded itself")
	}

	// The rolled-back transaction took the CREATE with it, and the connection is
	// usable afterwards — a leaked transaction would block every later writer.
	var tables int
	err = db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='ok_so_far'",
	).Scan(&tables)
	if err != nil {
		t.Fatalf("read sqlite_master after the rollback: %v", err)
	}
	if tables != 0 {
		t.Error("a failed change left its DDL behind")
	}
}

func TestApply_RejectsAnUnsupportedExtension(t *testing.T) {
	db, dbPath := openDB(t)
	path := writeChange(t, "e-2088-notes.md", "# not a change\n")

	_, err := schemachange.Apply(db, dbPath, path, io.Discard)
	if err == nil {
		t.Fatal("Apply accepted a .md file as a schema change")
	}
	if !errorsIs(err, schemachange.ErrUnsupportedChange) {
		t.Errorf("error is not ErrUnsupportedChange: %v", err)
	}
}

func TestApply_RejectsAMissingFile(t *testing.T) {
	db, dbPath := openDB(t)

	_, err := schemachange.Apply(
		db, dbPath, dt.Filepath(filepath.Join(t.TempDir(), "gone.sql")), io.Discard,
	)
	if err == nil {
		t.Fatal("Apply accepted a change file that does not exist")
	}
	if !errorsIs(err, schemachange.ErrChangeNotFound) {
		t.Errorf("error is not ErrChangeNotFound: %v", err)
	}
}

// TestApply_GoChangeRunsAsASubprocessAgainstTheSameDatabase proves the .go half,
// including the part that has actually gone wrong in the past: the script must
// open the database the PARENT resolved, not one it picks itself. The fixture
// reads ChangeDBEnvVar and writes there, so a parent that failed to pass it
// would leave the assertions below looking at an untouched database.
func TestApply_GoChangeRunsAsASubprocessAgainstTheSameDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a program with `go run`")
	}
	db, dbPath := openDB(t)

	// A .go change, in the shape internal/schema/changes/*.go take: a
	// `package main` program gated behind `//go:build ignore` so `go build
	// ./...` skips it, run by file name. This one does not import the runner
	// package — the point under test is the parent's half of the contract (the
	// path it passes and the marker it expects), and a fixture that reached into
	// the endless module would not compile from a temp directory.
	path := writeChange(t, "e-2088-via-go.go", `//go:build ignore

package main

import (
	"database/sql"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	path := os.Getenv("ENDLESS_CHANGE_DB")
	if path == "" {
		log.Fatal("ENDLESS_CHANGE_DB was not passed to the change script")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS _schema_version (name TEXT PRIMARY KEY, applied_at TEXT)")
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE from_go (id INTEGER PRIMARY KEY)")
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO from_go (id) VALUES (7)")
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO _schema_version (name, applied_at) VALUES ('e-2088-via-go', 'now')")
	if err != nil {
		log.Fatal(err)
	}
}
`)

	res, err := schemachange.Apply(db, dbPath, path, os.Stderr)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Status != schemachange.StatusApplied {
		t.Errorf("status = %q, want %q", res.Status, schemachange.StatusApplied)
	}

	var id int
	err = db.QueryRow("SELECT id FROM from_go").Scan(&id)
	if err != nil {
		t.Fatalf("the .go change did not reach the parent's database: %v", err)
	}
	if id != 7 {
		t.Errorf("from_go id = %d, want 7", id)
	}
	if markerCount(t, db, "e-2088-via-go") != 1 {
		t.Error("the .go change did not record its own marker")
	}
}

// TestApply_GoChangeFailureIsReported: the subprocess's non-zero exit becomes an
// error rather than a silent success, which is what lets a land surface it as a
// post-merge failure the operator can re-run.
func TestApply_GoChangeFailureIsReported(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a program with `go run`")
	}
	db, dbPath := openDB(t)
	path := writeChange(t, "e-2088-go-fails.go", `//go:build ignore

package main

import "os"

func main() {
	os.Exit(1)
}
`)

	_, err := schemachange.Apply(db, dbPath, path, io.Discard)
	if err == nil {
		t.Fatal("Apply succeeded on a .go change that exited non-zero")
	}
	if markerCount(t, db, "e-2088-go-fails") != 0 {
		t.Error("a failed .go change was recorded as applied")
	}
}

// errorsIs keeps the sentinel assertions readable without importing errors into
// every test above.
func errorsIs(err, target error) bool {
	return err != nil && target != nil && strings.Contains(err.Error(), target.Error())
}

// openDBAt opens (and so creates) a database at an exact path, for the tests
// that must hand that same path to the executable on a command line.
func openDBAt(t *testing.T, path string) (db *sql.DB, err error) {
	t.Helper()

	db, err = sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("close %s: %v", path, closeErr)
		}
	})
	db.SetMaxOpenConns(1)
	err = db.Ping()
	if err != nil {
		return nil, err
	}
	return db, nil
}
