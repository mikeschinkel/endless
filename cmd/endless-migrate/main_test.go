package main_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// migrateCmdPkg is ED-1571's migration-only executable, by module path so
// `go list` and `go build` resolve it from anywhere inside the module.
const migrateCmdPkg = "github.com/mikeschinkel/endless/cmd/endless-migrate"

// endlessPkgPrefix is what makes a dependency THIS project's code rather than a
// library's. The allowlist below is over these only: go-dt, doterr and the
// sqlite driver are leaf libraries and carry no schema expectations.
const endlessPkgPrefix = "github.com/mikeschinkel/endless/"

// allowedEndlessDeps is the complete set of Endless packages the migration
// executable may link. It is an ALLOWLIST rather than a list of forbidden
// packages on purpose: a denylist protects only against the imports someone
// thought of, and the property here is "nothing but migrations", which only an
// allowlist can state.
var allowedEndlessDeps = map[string]bool{
	migrateCmdPkg: true,
	"github.com/mikeschinkel/endless/internal/dbcontext": true,
	// E-2192: `up` runs the versioned migration set. internal/schema is that
	// set (embedded) plus the seeds, and links no Endless package but its own
	// migrations subpackage — migration machinery by any reading, which is the
	// only reason either is here. (E-2158 removed the per-ticket script
	// applier that once sat beside them.)
	"github.com/mikeschinkel/endless/internal/schema":            true,
	"github.com/mikeschinkel/endless/internal/schema/migrations": true,

	// E-2159. The executable refuses things — a relative --db path, a database
	// that is not there — and every refusal Endless writes must now say whether
	// the agent reading it has to report it. That is internal/refusal's job, and
	// it brings internal/agentenv with it for the one question it asks: is a
	// person or an agent reading this?
	//
	// Admitted rather than worked around because both ARE leaves, in the same
	// sense internal/dbcontext is. Between them they import os, io, fmt, log,
	// strings, errors and path/filepath, and nothing else — no database handle,
	// no schema, no task or session surface. The property this test defends is
	// "carries no code that expects a schema", and neither of them does, so
	// there is nothing here to move into a leaf package: they already are one.
	//
	// The loop below is over `go list -deps`, which is transitive, so this pair
	// cannot smuggle a fourth package in later without failing right here.
	"github.com/mikeschinkel/endless/internal/refusal":  true,
	"github.com/mikeschinkel/endless/internal/agentenv": true,
}

// TestMigrateExecutable_LinksNothingButTheMigrationMachinery is the guard on
// ED-1571's actual claim. The executable is safe to point at the real ledger
// during a self_dev land — where ED-1567 forbids the candidate endless-go — for
// one reason: it carries no code that expects a schema, so there is nothing the
// schema can disappoint.
//
// That is a property of the BUILD, not of the command line. A single import of
// internal/monitor would pull in the application's schema-applying connect, the
// enum integrity gates and the whole task/session surface, and every
// command-line assertion in this file would still pass. So the dependency graph
// is asserted directly.
//
// If this fails, the fix is to move what the new code needed into a leaf package
// (internal/dbcontext exists because path resolution was needed and
// internal/monitor was where it lived), never to widen the allowlist.
func TestMigrateExecutable_LinksNothingButTheMigrationMachinery(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", migrateCmdPkg).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", migrateCmdPkg, err)
	}

	var linked []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg := strings.TrimSpace(line)
		if !strings.HasPrefix(pkg, endlessPkgPrefix) && pkg != migrateCmdPkg {
			continue
		}
		linked = append(linked, pkg)
		if !allowedEndlessDeps[pkg] {
			t.Errorf("the migration executable links %s, which is not "+
				"migration machinery", pkg)
		}
	}

	// The allowlist must also be satisfied, not merely not violated: an empty
	// result would pass the loop above while proving nothing.
	if len(linked) != len(allowedEndlessDeps) {
		t.Errorf("linked Endless packages = %v, want exactly %d of them",
			linked, len(allowedEndlessDeps))
	}
}

// buildMigrate builds the executable into a scratch directory so the surface
// assertions below run against a binary from THIS tree.
func buildMigrate(t *testing.T) string {
	t.Helper()

	if testing.Short() {
		t.Skip("builds cmd/endless-migrate")
	}
	binary := filepath.Join(t.TempDir(), "endless-migrate")
	cmd := exec.Command("go", "build", "-o", binary, migrateCmdPkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", migrateCmdPkg, err, out)
	}
	return binary
}

// run invokes the built executable with an isolated, EMPTY config directory, so
// nothing here can reach a real database whatever the assertion is about.
func run(t *testing.T, binary string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runWithEnv(t, binary, []string{"XDG_CONFIG_HOME=" + t.TempDir()}, args...)
}

// runWithEnv is run with the environment named outright, for the cases that
// turn on WHICH variable is set: `--db main` follows $HOME and ignores
// XDG_CONFIG_HOME, so proving that needs both set to different places.
func runWithEnv(t *testing.T, binary string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), env...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case asExitError(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("running %s %v: %v", binary, args, err)
	}
	return outBuf.String(), errBuf.String(), code
}

func asExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// TestMigrateExecutable_HasNoApplicationSurface is the other half of the claim,
// and the half a reader can check by hand. "It never serves a hook, never runs a
// task command, never touches business data outside a migration" (ED-1571) is
// what makes it immune to the binary-expects-a-schema failure, and it is
// otherwise untested — every one of these subcommands exists on endless-go, and
// each is a thing this binary must refuse to be.
func TestMigrateExecutable_HasNoApplicationSurface(t *testing.T) {
	binary := buildMigrate(t)

	for _, sub := range []string{
		"hook", "event", "task", "session", "session-query", "session-state",
		"tmux", "worktree", "project", "db", "verify", "jobs", "spawn",
		// E-2158 retired the per-ticket scripts this applied.
		"apply",
	} {
		t.Run(sub, func(t *testing.T) {
			stdout, stderr, code := run(t, binary, sub)
			if code == 0 {
				t.Fatalf("%q was accepted; it must not exist on this binary", sub)
			}
			if !strings.Contains(stderr, "unknown command") {
				t.Errorf("%q was rejected, but not as an unknown command: %q",
					sub, stderr)
			}
			if stdout != "" {
				t.Errorf("%q wrote to stdout: %q", sub, stdout)
			}
		})
	}
}

// TestMigrateExecutable_OffersOnlyUp pins the surface from the other
// direction: whatever else changes, `up` (E-2192) is the only command the help
// text advertises, so a later subcommand cannot arrive documented-but-unnoticed.
//
// Read from the Commands block rather than by grepping the whole page, because
// the page deliberately says the words "hook", "task" and "query" in the
// sentence that disclaims them — and a check that cannot tell an offer from a
// disclaimer would force the disclaimer out to stay green.
func TestMigrateExecutable_OffersOnlyUp(t *testing.T) {
	binary := buildMigrate(t)

	stdout, _, code := run(t, binary, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d", code)
	}

	commands := helpCommands(stdout)
	if strings.Join(commands, " ") != "up" {
		t.Errorf("help offers commands %v, want exactly [up]:\n%s",
			commands, stdout)
	}
}

// helpCommands reads the command names out of the usage page's "Commands:"
// block: the first word of each indented line, until the block ends at a blank
// line. Continuation lines are indented further and start no command, which is
// what the column check below distinguishes.
func helpCommands(help string) (names []string) {
	const nameColumn = 2

	inBlock := false
	for _, line := range strings.Split(help, "\n") {
		if strings.TrimSpace(line) == "Commands:" {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent != nameColumn {
			continue
		}
		names = append(names, strings.Fields(line)[0])
	}
	return names
}

// TestMigrateExecutable_DBMainFollowsXDGThenHOME is the flag the land
// actually threads (E-2157), under E-2186's rule for where main lives:
// $XDG_CONFIG_HOME/endless when the user set it, else $HOME/.config/endless.
// It runs with HOME and XDG_CONFIG_HOME pointing at DIFFERENT directories, each
// holding a database, and asserts the XDG one moved and the HOME one did not —
// then that HOME wins once XDG is unset. A resolver that disagreed with the
// application's default would migrate one database and leave the user running
// on another.
func TestMigrateExecutable_DBMainFollowsXDGThenHOME(t *testing.T) {
	binary := buildMigrate(t)

	home := t.TempDir()
	xdg := t.TempDir()

	homeDB := filepath.Join(home, ".config", "endless", "endless.db")
	xdgDB := filepath.Join(xdg, "endless", "endless.db")
	dbs := map[string]*sql.DB{}
	for _, path := range []string{homeDB, xdgDB} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		db, err := openDBAt(t, path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		dbs[path] = db
	}
	hasTable := func(path, table string) bool {
		var name string
		return dbs[path].QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name) == nil
	}

	stdout, stderr, code := runWithEnv(t, binary,
		[]string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg},
		"--db", "main", "up")
	if code != 0 {
		t.Fatalf("up exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, xdgDB) {
		t.Errorf("--db main with XDG_CONFIG_HOME set did not open %s: %s", xdgDB, stdout)
	}
	// goose_db_version appears in exactly the database `up` migrated.
	if !hasTable(xdgDB, "goose_db_version") || hasTable(homeDB, "goose_db_version") {
		t.Error("--db main with XDG_CONFIG_HOME set migrated the wrong database")
	}

	stdout, stderr, code = runWithEnv(t, binary,
		[]string{"HOME=" + home, "XDG_CONFIG_HOME="},
		"--db", "main", "up")
	if code != 0 {
		t.Fatalf("up exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !hasTable(homeDB, "goose_db_version") {
		t.Error("--db main with XDG_CONFIG_HOME unset did not migrate the database under HOME")
	}
}

// TestMigrateExecutable_RefusesDBSandbox is this executable's ONE deliberate
// departure from endless-go's vocabulary (ED-1571): it resolves its target from
// what the caller named, never from where the caller is standing, so there is
// no "which sandbox" for it to answer.
//
// The refusal must NAME the remedy. The reader who types this is exactly the
// reader who learned `--db main|sandbox` from the guide and had no way to know
// this binary differs; "unknown command" would tell them nothing.
func TestMigrateExecutable_RefusesDBSandbox(t *testing.T) {
	binary := buildMigrate(t)

	_, stderr, code := run(t, binary, "--db", "sandbox", "up")
	if code == 0 {
		t.Fatal("--db sandbox was accepted; it has no cwd routing to resolve it with")
	}
	for _, want := range []string{"sandbox", "--db-dir", "--db main"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not mention %q: %s", want, stderr)
		}
	}
}

// TestMigrateExecutable_RefusesRetiredConfigDirFlag: --config-dir was this
// binary's flag until E-2157 renamed it, so a stale invocation is muscle memory
// rather than a typo, and the refusal names the replacement instead of leaving
// the reader to guess from "unknown command".
func TestMigrateExecutable_RefusesRetiredConfigDirFlag(t *testing.T) {
	binary := buildMigrate(t)

	cfgDir := t.TempDir()
	_, stderr, code := run(t, binary, "--config-dir", cfgDir, "up")
	if code == 0 {
		t.Fatal("--config-dir was accepted; E-2157 retired it")
	}
	if !strings.Contains(stderr, "--db-dir") {
		t.Errorf("the refusal does not name the replacement flag: %s", stderr)
	}
}

// upDB creates an empty database file in a fresh config directory and returns
// both. A zero-byte file is an empty SQLite database at goose version 0 — as
// far behind the embedded set as a database can be.
func upDB(t *testing.T) (cfgDir, dbPath string) {
	t.Helper()

	cfgDir = t.TempDir()
	dbPath = filepath.Join(cfgDir, "endless.db")
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatalf("create %s: %v", dbPath, err)
	}
	return cfgDir, dbPath
}

// runUp runs `up` against cfgDir and decodes its document.
func runUp(t *testing.T, binary, cfgDir string) (doc map[string]any) {
	t.Helper()

	stdout, stderr, code := run(t, binary, "--db-dir", cfgDir, "up")
	if code != 0 {
		t.Fatalf("up exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("up printed no JSON document: %v\n%s", err, stdout)
	}
	return doc
}

// TestMigrateExecutable_UpBringsABehindDatabaseToLatestAndSeedsIt is E-2192's
// mechanism through its real command line: a database behind the embedded set
// reaches LatestVersion, and the enum mirrors are seeded — `up` is
// schema.Migrate, not goose Up alone, so the database it leaves passes the
// integrity gates the recording binary's connect reads next.
func TestMigrateExecutable_UpBringsABehindDatabaseToLatestAndSeedsIt(t *testing.T) {
	binary := buildMigrate(t)
	cfgDir, dbPath := upDB(t)

	latest, err := schema.LatestVersion()
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}

	doc := runUp(t, binary, cfgDir)
	if doc["status"] != "migrated" {
		t.Errorf("status = %v, want migrated", doc["status"])
	}
	if doc["from"] != float64(0) || doc["to"] != float64(latest) {
		t.Errorf("from/to = %v/%v, want 0/%d", doc["from"], doc["to"], latest)
	}
	if doc["db"] != dbPath {
		t.Errorf("db = %v, want %s", doc["db"], dbPath)
	}

	db, err := openDBAt(t, dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	var slug string
	err = db.QueryRow("SELECT slug FROM task_types WHERE id = 2").Scan(&slug)
	if err != nil || slug != "bugfix" {
		t.Errorf("the enum mirrors were not seeded: slug=%q err=%v", slug, err)
	}

	// Part-way behind, the E-2188 shape: stamped one version short of latest.
	// The newest step is idempotent by the migration set's own rule, so
	// forgetting its stamp is enough to put the database one behind.
	_, err = db.Exec("DELETE FROM goose_db_version WHERE version_id = ?", latest)
	if err != nil {
		t.Fatalf("un-stamping version %d: %v", latest, err)
	}
	_, err = db.Exec("DELETE FROM task_types WHERE id = 2")
	if err != nil {
		t.Fatalf("removing a mirror row: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	doc = runUp(t, binary, cfgDir)
	if doc["from"] != float64(latest-1) || doc["to"] != float64(latest) {
		t.Errorf("from/to = %v/%v, want %d/%d", doc["from"], doc["to"], latest-1, latest)
	}
	db, err = openDBAt(t, dbPath)
	if err != nil {
		t.Fatalf("reopen %s: %v", dbPath, err)
	}
	err = db.QueryRow("SELECT slug FROM task_types WHERE id = 2").Scan(&slug)
	if err != nil || slug != "bugfix" {
		t.Errorf("up did not re-seed the enum mirrors: slug=%q err=%v", slug, err)
	}
}

// TestMigrateExecutable_UpOnACurrentDatabaseIsANoOp: every self_dev land runs
// `up`, so on the ordinary land — no migration on the branch — it must change
// nothing and say so.
func TestMigrateExecutable_UpOnACurrentDatabaseIsANoOp(t *testing.T) {
	binary := buildMigrate(t)
	cfgDir, _ := upDB(t)

	runUp(t, binary, cfgDir)
	doc := runUp(t, binary, cfgDir)
	if doc["status"] != "current" {
		t.Errorf("status = %v, want current", doc["status"])
	}
	if doc["from"] != doc["to"] {
		t.Errorf("a current database moved: from %v to %v", doc["from"], doc["to"])
	}
}

// TestMigrateExecutable_UpRefusesADatabaseThatDoesNotExist: a migration that
// creates its target has migrated nothing. `up` on a missing file would build a
// complete, empty ledger and report success.
func TestMigrateExecutable_UpRefusesADatabaseThatDoesNotExist(t *testing.T) {
	binary := buildMigrate(t)
	cfgDir := t.TempDir()

	stdout, _, code := run(t, binary, "--db-dir", cfgDir, "up")
	if code == 0 {
		t.Fatal("up succeeded against a config dir holding no database")
	}
	if !strings.Contains(stdout, "no database at") {
		t.Errorf("the refusal does not say the database is missing: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "endless.db")); err == nil {
		t.Error("the refusal created the database it was refusing to migrate")
	}
}

// TestMigrateExecutable_UpRefusesADatabaseOlderThanBothVersioningSchemes is
// E-2158's refusal through the real command line: a database with Endless data,
// no goose_db_version and a user_version the retired Python ladder never
// finished is refused, and the file is left exactly as it was.
func TestMigrateExecutable_UpRefusesADatabaseOlderThanBothVersioningSchemes(t *testing.T) {
	binary := buildMigrate(t)
	cfgDir, dbPath := upDB(t)

	db, err := openDBAt(t, dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	_, err = db.Exec(`CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT);
		PRAGMA user_version = 5`)
	if err != nil {
		t.Fatalf("building the fixture: %v", err)
	}
	before := masterRows(t, db)

	stdout, _, code := run(t, binary, "--db-dir", cfgDir, "up")
	if code == 0 {
		t.Fatalf("up accepted a pre-versioning database: %s", stdout)
	}
	for _, want := range []string{dbPath, "user_version 5", "too old"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the refusal does not mention %q: %s", want, stdout)
		}
	}
	if after := masterRows(t, db); after != before {
		t.Errorf("the refusal changed the database\nbefore: %s\nafter:  %s", before, after)
	}
}

// masterRows renders sqlite_master as one comparable string.
func masterRows(t *testing.T, db *sql.DB) string {
	t.Helper()

	rows, err := db.Query(`SELECT type, name, coalesce(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatalf("reading sqlite_master: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var typ, name, text string
		if err := rows.Scan(&typ, &name, &text); err != nil {
			t.Fatalf("scanning sqlite_master: %v", err)
		}
		fmt.Fprintf(&b, "%s %s %s\n", typ, name, text)
	}
	return b.String()
}

// openDBAt opens the database file at path with one connection, closed when
// the test ends.
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
