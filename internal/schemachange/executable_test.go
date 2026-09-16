package schemachange_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	"github.com/mikeschinkel/endless/internal/dbcontext":    true,
	"github.com/mikeschinkel/endless/internal/schemachange": true,
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

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
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

// TestMigrateExecutable_OffersOnlyApply pins the surface from the other
// direction: whatever else changes, `apply` is the only command the help text
// advertises, so a later subcommand cannot arrive documented-but-unnoticed.
//
// Read from the Commands block rather than by grepping the whole page, because
// the page deliberately says the words "hook", "task" and "query" in the
// sentence that disclaims them — and a check that cannot tell an offer from a
// disclaimer would force the disclaimer out to stay green.
func TestMigrateExecutable_OffersOnlyApply(t *testing.T) {
	binary := buildMigrate(t)

	stdout, _, code := run(t, binary, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d", code)
	}

	commands := helpCommands(stdout)
	if len(commands) != 1 || commands[0] != "apply" {
		t.Errorf("help offers commands %v, want exactly [apply]:\n%s",
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

// TestMigrateExecutable_RefusesADatabaseThatDoesNotExist: sql.Open would create
// one, and a migration that CREATES its target has migrated nothing — it has
// manufactured an empty file and reported success. A land that resolved the
// wrong path must fail, not quietly migrate a database nobody meant.
func TestMigrateExecutable_RefusesADatabaseThatDoesNotExist(t *testing.T) {
	binary := buildMigrate(t)

	change := writeChange(t, "e-2088-unused.sql", "CREATE TABLE thing (id INTEGER);\n")
	cfgDir := t.TempDir()

	stdout, _, code := run(t, binary, "--config-dir", cfgDir, "apply", string(change))
	if code == 0 {
		t.Fatal("apply succeeded against a config dir holding no database")
	}
	if !strings.Contains(stdout, "no database at") {
		t.Errorf("the refusal does not say the database is missing: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "endless.db")); err == nil {
		t.Error("the refusal created the database it was refusing to migrate")
	}
}

// TestMigrateExecutable_AppliesAChangeEndToEnd is the executable doing its one
// job, through its real command line, against a database on disk: version
// before, version after, the DML committed, and the second run a skip.
func TestMigrateExecutable_AppliesAChangeEndToEnd(t *testing.T) {
	binary := buildMigrate(t)

	cfgDir := t.TempDir()
	dbPath := filepath.Join(cfgDir, "endless.db")
	db, err := openDBAt(t, dbPath)
	if err != nil {
		t.Fatalf("create %s: %v", dbPath, err)
	}

	change := writeChange(t, "e-2088-end-to-end.sql", `
		CREATE TABLE task_types (id INTEGER PRIMARY KEY, slug TEXT NOT NULL);
		INSERT OR IGNORE INTO task_types (id, slug) VALUES (1, 'todo');
	`)

	stdout, stderr, code := run(t, binary, "--config-dir", cfgDir, "apply", string(change))
	if code != 0 {
		t.Fatalf("apply exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"status":"applied"`) {
		t.Errorf("apply did not report applied: %q", stdout)
	}
	// The result names the database it opened, so a caller can check the one
	// thing worth checking about a migration tool.
	if !strings.Contains(stdout, dbPath) {
		t.Errorf("the result does not name the database it changed: %q", stdout)
	}

	var slug string
	err = db.QueryRow("SELECT slug FROM task_types WHERE id = 1").Scan(&slug)
	if err != nil {
		t.Fatalf("the change's DML did not commit: %v", err)
	}
	if slug != "todo" {
		t.Errorf("slug = %q, want %q", slug, "todo")
	}
	if markerCount(t, db, "e-2088-end-to-end") != 1 {
		t.Error("the change was applied but not recorded")
	}

	stdout, _, code = run(t, binary, "--config-dir", cfgDir, "apply", string(change))
	if code != 0 {
		t.Fatalf("re-apply exited %d: %s", code, stdout)
	}
	if !strings.Contains(stdout, `"status":"skipped"`) {
		t.Errorf("re-applying did not skip: %q", stdout)
	}
}
