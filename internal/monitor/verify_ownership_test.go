package monitor

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// The guard opens a database this binary does not own. It must not be able to
// write to it — not by mistake, and not by a later edit that adds a statement
// nobody notices is a write. Read-only is the structural version of that
// promise, so it is pinned here rather than left to review.
func TestSuiteOwnershipDB_OpensReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "endless")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	seed, err := sql.Open("sqlite", filepath.Join(dir, "endless.db"))
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	if _, err = seed.Exec("CREATE TABLE task_landings (id INTEGER PRIMARY KEY, task_id INTEGER)"); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	seed.Close()

	db, err := suiteOwnershipDB()
	if err != nil || db == nil {
		t.Fatalf("suiteOwnershipDB: %v (db=%v)", err, db)
	}
	defer db.Close()

	if _, err = db.Exec("CREATE TABLE intruder (x)"); err == nil {
		t.Fatal("the guard's handle accepted a write to the main database")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Errorf("write was refused, but not as read-only: %v", err)
	}
}

// A path the read-only URI form would misread is opened plainly. A # in the
// path does not error in URI form — it silently opens a different, empty
// database, which would disable the guard without saying so.
func TestReadOnlyDSN(t *testing.T) {
	cases := map[string]string{
		"/Users/mike/.config/endless/endless.db":    "file:/Users/mike/.config/endless/endless.db?mode=ro",
		"/Users/Mike Schinkel/.config/e/endless.db": "file:/Users/Mike Schinkel/.config/e/endless.db?mode=ro",
		"/Users/m#ike/.config/endless/endless.db":   "/Users/m#ike/.config/endless/endless.db",
		"/Users/m?ike/.config/endless/endless.db":   "/Users/m?ike/.config/endless/endless.db",
	}
	for in, want := range cases {
		if got := readOnlyDSN(in); got != want {
			t.Errorf("readOnlyDSN(%q) = %q, want %q", in, got, want)
		}
	}
}

// A machine with no main database yet must not have one created as a side
// effect of a guard that only wanted to read it.
func TestSuiteOwnershipDB_NeverCreatesTheDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	db, err := suiteOwnershipDB()
	if err != nil {
		t.Fatalf("suiteOwnershipDB: %v", err)
	}
	if db != nil {
		db.Close()
		t.Fatal("opened a handle when no database exists")
	}
	if _, err = os.Stat(filepath.Join(home, ".config", "endless", "endless.db")); err == nil {
		t.Error("the guard created the main database")
	}
}
