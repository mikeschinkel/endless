package monitor

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// The connect-time schema rules (E-2020, ED-1570, ED-1601). The pure rules are
// proven as tables; the rest drive the real DB() singleton against on-disk
// databases.

func TestConnectActionFor(t *testing.T) {
	cases := []struct {
		name      string
		dbVersion int64
		latest    int64
		want      connectAction
	}{
		{"behind applies forward", 9, 10, actionApply},
		{"a fresh file is behind", 0, 10, actionApply},
		{"equal is current", 10, 10, actionCurrent},
		{"ahead halts", 11, 10, actionHalt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := connectActionFor(c.dbVersion, c.latest); got != c.want {
				t.Errorf("connectActionFor(%d, %d) = %v, want %v", c.dbVersion, c.latest, got, c.want)
			}
		})
	}
}

// TestRefusesMainDB: a worktree build never opens main, and nothing else is
// refused on these grounds — the installed binary opens main, and a worktree
// build opens its own sandbox (where it is behind-and-applies like any owner).
func TestRefusesMainDB(t *testing.T) {
	cases := []struct {
		worktreeBuild, mainDB, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	}
	for _, c := range cases {
		if got := refusesMainDB(c.worktreeBuild, c.mainDB); got != c.want {
			t.Errorf("refusesMainDB(worktreeBuild=%v, mainDB=%v) = %v, want %v",
				c.worktreeBuild, c.mainDB, got, c.want)
		}
	}
}

// resetDBSingleton saves every DB-routing global, resets the singleton so DB()
// runs its body, and restores all of it on cleanup.
func resetDBSingleton(t *testing.T) {
	t.Helper()
	prevOnce, prevConn, prevErr, prevFault := dbOnce, dbConn, dbErr, faultConn
	prevCtxDir, prevPathOverride, prevExe := dbContextDir, dbPathOverride, osExecutable
	t.Cleanup(func() {
		if dbConn != nil {
			dbConn.Close()
		}
		if faultConn != nil {
			faultConn.Close()
		}
		dbOnce, dbConn, dbErr, faultConn = prevOnce, prevConn, prevErr, prevFault
		dbContextDir, dbPathOverride, osExecutable = prevCtxDir, prevPathOverride, prevExe
	})
	dbOnce = &sync.Once{}
	dbConn, dbErr, faultConn = nil, nil, nil
	dbContextDir, dbPathOverride = "", ""
}

// openDBAtPath drives DB() against path through an explicit --db-dir-style
// context, which satisfies the E-1429 gate even though the test runs inside a
// self-dev worktree.
func openDBAtPath(t *testing.T, path string) (*sql.DB, error) {
	t.Helper()
	resetDBSingleton(t)
	dbContextDir = filepath.Dir(path)
	return DB()
}

// playWorktreeBuild makes candidateBuild() true for the rest of the test.
func playWorktreeBuild(t *testing.T) {
	t.Helper()
	osExecutable = func() (string, error) {
		return "/Users/x/Projects/endless/.endless/worktrees/e-2020/bin/endless-go", nil
	}
}

// mainDBUnderTempHome points $HOME at a temp dir and returns the main
// database path inside it — what dbcontext.MainDBPath() now resolves to.
func mainDBUnderTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Since E-2186 the main database follows $XDG_CONFIG_HOME/endless before
	// $HOME/.config/endless, so a caller's XDG_CONFIG_HOME (the verify runner
	// sets one) would send the pinned connect to a different database than the
	// one this helper hands back. Empty means unset to that resolver.
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "endless")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "endless.db")
}

// buildAt creates a database at path migrated to exactly version (0 = latest).
func buildAt(t *testing.T, path string, version int64) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if version == 0 {
		err = schema.Migrate(db)
	} else {
		err = schema.MigrateToContext(context.Background(), db, version)
	}
	if err != nil {
		t.Fatalf("building the database at version %d: %v", version, err)
	}
}

func dbVersionAt(t *testing.T, path string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v, err := schema.DBVersion(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func schemaText(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT type, name, coalesce(sql, '') FROM sqlite_master ORDER BY type, name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out string
	for rows.Next() {
		var typ, name, text string
		if err := rows.Scan(&typ, &name, &text); err != nil {
			t.Fatal(err)
		}
		out += typ + " " + name + " " + text + "\n"
	}
	return out
}

func latestVersion(t *testing.T) int64 {
	t.Helper()
	v, err := schema.LatestVersion()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDB_WorktreeBuildRefusesMainAtEveryVersion(t *testing.T) {
	path := mainDBUnderTempHome(t)
	buildAt(t, path, latestVersion(t)-1)
	before := schemaText(t, path)

	resetDBSingleton(t)
	playWorktreeBuild(t)
	PinMainDB()

	_, err := DB()
	var refusal *SchemaRefusal
	if !errors.As(err, &refusal) || refusal.Kind != "worktree-build-on-main" {
		t.Fatalf("DB() = %v, want a worktree-build-on-main refusal", err)
	}
	if !errors.Is(err, ErrSchemaRefused) {
		t.Error("the refusal does not match ErrSchemaRefused")
	}
	if faultConn != nil {
		t.Error("a worktree build refused main but still opened it for the fault writer")
	}
	if after := schemaText(t, path); after != before {
		t.Error("a refused worktree build changed the main database's schema")
	}
	if got := dbVersionAt(t, path); got != latestVersion(t)-1 {
		t.Errorf("main is at version %d after the refusal, want it untouched at %d", got, latestVersion(t)-1)
	}
}

func TestDB_WorktreeBuildMigratesItsOwnBehindDatabase(t *testing.T) {
	mainDBUnderTempHome(t)
	path := filepath.Join(t.TempDir(), "endless.db")
	buildAt(t, path, latestVersion(t)-1)

	resetDBSingleton(t)
	playWorktreeBuild(t)
	dbContextDir = filepath.Dir(path)

	if _, err := DB(); err != nil {
		t.Fatalf("a worktree build on its own database must migrate it forward, got: %v", err)
	}
	if got := dbVersionAt(t, path); got != latestVersion(t) {
		t.Errorf("version after connect = %d, want %d", got, latestVersion(t))
	}
}

func TestDB_BehindIsBackedUpThenMigrated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.db")
	buildAt(t, path, latestVersion(t)-1)

	if _, err := openDBAtPath(t, path); err != nil {
		t.Fatalf("an installed binary on a behind database must migrate it, got: %v", err)
	}
	if got := dbVersionAt(t, path); got != latestVersion(t) {
		t.Errorf("version after connect = %d, want %d", got, latestVersion(t))
	}
	backups, err := listBackups(filepath.Join(filepath.Dir(path), "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("found %d backups beside the migrated database, want exactly 1", len(backups))
	}
	if got := dbVersionAt(t, filepath.Join(filepath.Dir(path), "backups", backups[0].Name)); got != latestVersion(t)-1 {
		t.Errorf("the backup is at version %d, want the pre-migration %d", got, latestVersion(t)-1)
	}
}

func TestDB_CurrentAppliesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.db")
	buildAt(t, path, 0)
	before := schemaText(t, path)

	if _, err := openDBAtPath(t, path); err != nil {
		t.Fatal(err)
	}
	if after := schemaText(t, path); after != before {
		t.Error("a connect to a current database changed its schema")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "backups")); err == nil {
		t.Error("a connect to a current database took a backup")
	}
}

func TestDB_AheadHalts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.db")
	buildAt(t, path, 0)
	ahead := latestVersion(t) + 1
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)", ahead); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	before := schemaText(t, path)

	_, err = openDBAtPath(t, path)
	var refusal *SchemaRefusal
	if !errors.As(err, &refusal) || refusal.Kind != "database-ahead" {
		t.Fatalf("DB() = %v, want a database-ahead refusal", err)
	}
	if refusal.DBVersion != ahead || refusal.BinaryVersion != latestVersion(t) {
		t.Errorf("refusal names versions %d/%d, want %d/%d",
			refusal.DBVersion, refusal.BinaryVersion, ahead, latestVersion(t))
	}
	if after := schemaText(t, path); after != before {
		t.Error("a halted connect changed the schema")
	}
	// The fault writer still gets the connection, so a land window's hooks
	// record ONE deduplicated incident rather than a log-only line each.
	if db, err := FaultDB(); err != nil || db == nil {
		t.Errorf("FaultDB() = (%v, %v), want the halted connection", db, err)
	}
}

// E-1659's self-heal and the fail-closed gates survive the removal of
// schema-passive: they now run on every connect that is not refused.
func TestDB_SeedAndGatesRunOnEveryConnect(t *testing.T) {
	t.Run("a drifted mirror row is reseeded on a current database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "endless.db")
		buildAt(t, path, 0)
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec("UPDATE task_types SET slug='task', label='Task' WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		raw.Close()

		db, err := openDBAtPath(t, path)
		if err != nil {
			t.Fatalf("a renamed mirror row must self-heal, got: %v", err)
		}
		var slug string
		if err := db.QueryRow("SELECT slug FROM task_types WHERE id=1").Scan(&slug); err != nil {
			t.Fatal(err)
		}
		if slug != "todo" {
			t.Errorf("task_types id=1 slug = %q after connect, want the reseeded %q", slug, "todo")
		}
	})

	t.Run("a drifted gate_kinds row is reseeded too", func(t *testing.T) {
		// E-2020: gate_kinds was INSERT OR IGNORE, so this drift used to
		// fail-close every connect with nothing able to repair it.
		path := filepath.Join(t.TempDir(), "endless.db")
		buildAt(t, path, 0)
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec("UPDATE gate_kinds SET slug='drifted', label='Drifted' WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		raw.Close()

		db, err := openDBAtPath(t, path)
		if err != nil {
			t.Fatalf("a drifted gate_kinds row must self-heal, got: %v", err)
		}
		var slug string
		if err := db.QueryRow("SELECT slug FROM gate_kinds WHERE id=1").Scan(&slug); err != nil {
			t.Fatal(err)
		}
		if slug != "revisit" {
			t.Errorf("gate_kinds id=1 slug = %q after connect, want %q", slug, "revisit")
		}
	})

	t.Run("a rogue mirror row fail-closes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "endless.db")
		buildAt(t, path, 0)
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec("INSERT INTO task_types (id, slug, label) VALUES (99, 'rogue', 'Rogue')"); err != nil {
			t.Fatal(err)
		}
		raw.Close()

		if _, err := openDBAtPath(t, path); err == nil {
			t.Fatal("a task_types row with no enum constant must fail-close the connect")
		}
	})

	t.Run("the hook's pin no longer opens schema-passive", func(t *testing.T) {
		path := mainDBUnderTempHome(t)
		buildAt(t, path, 0)
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec("INSERT INTO task_types (id, slug, label) VALUES (99, 'rogue', 'Rogue')"); err != nil {
			t.Fatal(err)
		}
		raw.Close()

		resetDBSingleton(t)
		PinMainDB()
		if _, err := DB(); err == nil {
			t.Fatal("the pinned (hook) path skipped the integrity gates; schema-passive is supposed to be gone")
		}
	})
}

// E-2205: a land finds the ERR-0020 its own migration caused by rebuilding the
// fingerprint from the two versions alone. It must be the very fingerprint a
// recorded database-ahead refusal carries, or the land clears nothing.
func TestDatabaseAheadFingerprint_MatchesTheRecordedRefusal(t *testing.T) {
	got := DatabaseAheadFingerprint(12, 11)
	want := databaseAheadRefusal("/any/endless.db", 12, 11).Fingerprint()
	if got != want {
		t.Errorf("DatabaseAheadFingerprint(12, 11) = %q, want %q", got, want)
	}
	if want != "database-ahead:database is at schema v12, endless-go carries v11: upgrade endless" {
		t.Errorf("the recorded fingerprint changed shape: %q", want)
	}
}
