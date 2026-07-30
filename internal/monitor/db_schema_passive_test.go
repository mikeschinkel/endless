package monitor

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// openDBAtPath drives the real monitor.DB() singleton against an on-disk DB at
// path, with the process-global DB-context vars set to model either the
// foreign-real-DB pin (pinned=true -> dbPathOverride=path, the ForceRealDB /
// PinMainDB state) or the owner path (pinned=false -> dbContextDir=dir, the
// deployed/self-detected open of a DB the binary owns). It resets the dbOnce
// singleton so DB() runs its body, and restores every global on cleanup so
// sequential tests don't leak. Both states set an explicit context, so
// guardWorktreeDBContext() lets the open proceed even though the test runs from
// inside this self-dev worktree.
func openDBAtPath(t *testing.T, path string, pinned bool) (*sql.DB, error) {
	t.Helper()

	prevOnce, prevConn, prevErr := dbOnce, dbConn, dbErr
	prevCtxDir, prevPathOverride, prevFromFlag := dbContextDir, dbPathOverride, dbContextFromFlag
	t.Cleanup(func() {
		if dbConn != nil {
			dbConn.Close()
		}
		dbOnce = prevOnce
		dbConn = prevConn
		dbErr = prevErr
		dbContextDir = prevCtxDir
		dbPathOverride = prevPathOverride
		dbContextFromFlag = prevFromFlag
	})

	dbOnce = &sync.Once{}
	dbConn = nil
	dbErr = nil
	dbContextDir = ""
	dbPathOverride = ""
	dbContextFromFlag = false
	if pinned {
		dbPathOverride = path
	} else {
		dbContextDir = filepath.Dir(path)
	}
	return DB()
}

// seedTaskTypesOnly creates a standalone task_types table (and nothing else) at
// path with a single row whose slug DIFFERS from the running tasktype enum
// (id=1 'todo' vs. the enum's 'task'). This models the E-1659 incident: a real
// DB whose enum mirror diverges from a candidate worktree binary. The absence
// of the `tasks` table is the schema-passive probe — schema.SQL would create
// it, so its continued absence after an open proves schema.SQL never ran.
func seedTaskTypesOnly(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE task_types (id INTEGER PRIMARY KEY, slug TEXT UNIQUE NOT NULL, label TEXT NOT NULL)`,
		`INSERT INTO task_types (id, slug, label) VALUES (1, 'todo', 'Todo')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed exec %q: %v", s, err)
		}
	}
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", name,
	).Scan(&n); err != nil {
		t.Fatalf("tableExists(%q): %v", name, err)
	}
	return n > 0
}

func taskTypeSlug(t *testing.T, db *sql.DB, id int) string {
	t.Helper()
	var slug string
	if err := db.QueryRow("SELECT slug FROM task_types WHERE id=?", id).Scan(&slug); err != nil {
		t.Fatalf("read task_types slug id=%d: %v", id, err)
	}
	return slug
}

// TestDBSchemaPassiveOnRealDBPin is the E-1818 regression: a candidate binary
// pinned to a real DB it does not own (dbPathOverride != "") must open the DB
// schema-passive — no schema.SQL exec, no enum integrity gate. Before the fix,
// DB() ran schema.SQL + VerifyIntegrity on the pinned path, so a drifted enum
// fail-closed (err != nil) and a destructive schema.SQL could rewrite rows the
// binary does not own. This is the exact vector that corrupted the real DB
// during E-1659.
func TestDBSchemaPassiveOnRealDBPin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.db")
	seedTaskTypesOnly(t, path)

	db, err := openDBAtPath(t, path, true /* pinned to foreign real DB */)
	if err != nil {
		t.Fatalf("pinned open must not fail-close on enum drift, got: %v", err)
	}
	if db == nil {
		t.Fatal("pinned open returned nil db without error")
	}
	// schema.SQL must NOT have run: the `tasks` table it would create is absent.
	if tableExists(t, db, "tasks") {
		t.Error("pinned open created the `tasks` table; schema.SQL must be skipped on a foreign real DB")
	}
	// The pre-existing drifted seed row must be byte-for-byte untouched.
	if got := taskTypeSlug(t, db, 1); got != "todo" {
		t.Errorf("task_types id=1 slug = %q, want %q (pinned open must not reconcile seed rows)", got, "todo")
	}
}

// TestDBOwnerPathMigratesAndVerifies pins nothing (dbPathOverride == ""), so
// DB() takes the owner path: schema.SQL applies and the enum integrity gate
// runs — the behavior land-time `apply-change` and `just install` rely on. Two
// cases prove the E-1818 gate did not weaken the owner path.
func TestDBOwnerPathMigratesAndVerifies(t *testing.T) {
	t.Run("fresh DB gets schema applied", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "endless.db")
		db, err := openDBAtPath(t, path, false /* owner path */)
		if err != nil {
			t.Fatalf("owner open of a fresh DB must succeed, got: %v", err)
		}
		if !tableExists(t, db, "tasks") {
			t.Error("owner open of a fresh DB did not create `tasks`; schema.SQL must run")
		}
		if got := taskTypeSlug(t, db, 1); got != "task" {
			t.Errorf("seeded task_types id=1 slug = %q, want %q", got, "task")
		}
	})

	t.Run("drifted enum still fail-closes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "endless.db")
		seedTaskTypesOnly(t, path) // id=1 'todo', diverges from the enum's 'task'
		_, err := openDBAtPath(t, path, false /* owner path */)
		if err == nil {
			t.Fatal("owner open must fail-close when task_types drifts from the enum")
		}
	})
}

// TestDBSchemaPassiveViaPinMainDB is the end-to-end form of the E-1818
// regression: it takes the pin through the REAL entry point PinMainDB() with a
// redirected HOME (so DBPath() resolves to $HOME/.config/endless/endless.db, the
// real ledger location) and opens a fully schema'd DB whose task_types slug has
// been diverged from the running enum — the exact E-1659 scenario. Before the
// fix the pinned open ran schema.SQL + VerifyIntegrity and fail-closed; after
// it, the open succeeds and the drifted row is untouched.
func TestDBSchemaPassiveViaPinMainDB(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbDir := filepath.Join(home, ".config", "endless")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dbDir, "endless.db")

	// Seed the full deployed schema, then diverge the task_types enum mirror so
	// the running binary's enum ('task') no longer matches the on-disk row
	// ('todo') — a real DB the candidate binary does not own.
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := seed.Exec(schema.SQL); err != nil {
		seed.Close()
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := seed.Exec("UPDATE task_types SET slug='todo', label='Todo' WHERE id=1"); err != nil {
		seed.Close()
		t.Fatalf("diverge task_types: %v", err)
	}
	seed.Close()

	// Save/restore globals and reset the singleton, then take the pin via the
	// real entry point rather than setting dbPathOverride by hand.
	prevOnce, prevConn, prevErr := dbOnce, dbConn, dbErr
	prevCtxDir, prevPathOverride, prevFromFlag := dbContextDir, dbPathOverride, dbContextFromFlag
	t.Cleanup(func() {
		if dbConn != nil {
			dbConn.Close()
		}
		dbOnce = prevOnce
		dbConn = prevConn
		dbErr = prevErr
		dbContextDir = prevCtxDir
		dbPathOverride = prevPathOverride
		dbContextFromFlag = prevFromFlag
	})
	dbOnce = &sync.Once{}
	dbConn = nil
	dbErr = nil
	dbContextDir = ""
	dbPathOverride = ""
	dbContextFromFlag = false

	PinMainDB() // dbPathOverride = $HOME/.config/endless/endless.db
	if got := DBPath(); got != path {
		t.Fatalf("PinMainDB() resolved DBPath()=%q, want the redirected real DB %q", got, path)
	}

	db, err := DB()
	if err != nil {
		t.Fatalf("pinned open of a divergent real DB must succeed, got: %v", err)
	}
	if got := taskTypeSlug(t, db, 1); got != "todo" {
		t.Errorf("task_types id=1 slug = %q, want %q (pin must not reconcile the real DB)", got, "todo")
	}
}
