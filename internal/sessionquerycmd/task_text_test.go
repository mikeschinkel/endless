package sessionquerycmd

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// endlessGoBinPath holds the path to the endless-go binary that
// TestMain built once for the package's whole test sweep. Per-test
// t.TempDir() would be cleaned up between tests; TestMain owns the
// directory for the lifetime of `go test`.
var endlessGoBinPath string

// TestMain builds the endless-go binary once before any test runs, then
// runs the test suite, then cleans up. The build cost is amortized
// across every binary-integration test in this package.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "endless-go-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: mkdirtemp: %v\n", err)
		os.Exit(2)
	}
	defer os.RemoveAll(dir)

	bin := filepath.Join(dir, "endless-go")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/endless-go")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build endless-go: %v\n%s\n", err, out)
		os.Exit(2)
	}
	endlessGoBinPath = bin

	os.Exit(m.Run())
}

// endlessGoBin returns the prebuilt binary path. TestMain is the single
// build site; this accessor exists so test bodies stay self-documenting.
func endlessGoBin(t *testing.T) string {
	t.Helper()
	if endlessGoBinPath == "" {
		t.Fatal("endless-go binary not built — TestMain did not run")
	}
	return endlessGoBinPath
}

// seedTaskDB writes an endless.db at $cfgDir/endless.db with the schema
// applied and one tasks row whose text is `text`. Returns the seeded task
// id. Use with --config-dir so the gate is satisfied and the binary opens
// THIS db.
func seedTaskDB(t *testing.T, cfgDir string, id int64, text string) {
	t.Helper()
	dbPath := filepath.Join(cfgDir, "endless.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema to seed db: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, 'proj-seed', '/tmp/proj-seed')",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status, text) VALUES (?, 1, ?, 'ready', ?)",
		id, "seed task", text,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

// TestTaskText_BinaryReadsSeededRow pins the happy path of the
// endless-go session-query task-text verb: given a task row with
// non-empty text, the binary prints exactly that text to stdout and
// exits 0. This is the contract create_task_worktree relies on to
// materialize plan files at claim time (E-894, E-1445).
func TestTaskText_BinaryReadsSeededRow(t *testing.T) {
	cfgDir := t.TempDir()
	want := "# Plan\n\nDo the thing.\n"
	seedTaskDB(t, cfgDir, 42, want)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "task-text", "--id", "42")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary exec failed: %v\nout: %s", err, out)
	}
	if string(out) != want {
		t.Errorf("stdout = %q, want %q", string(out), want)
	}
}

// TestTaskText_BinaryMissingRowExitsZeroEmpty pins the documented "no
// plan to materialize" contract: an unknown task id returns "" with
// exit 0 so the Python caller can run materialize uniformly for
// present-and-absent rows.
func TestTaskText_BinaryMissingRowExitsZeroEmpty(t *testing.T) {
	cfgDir := t.TempDir()
	seedTaskDB(t, cfgDir, 42, "present-row")

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "task-text", "--id", "9999999")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected exit 0 on missing row, got %v\nout: %s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("expected empty stdout on missing row, got %q", string(out))
	}
}

// TestTaskText_BinaryMissingIdFlagExitsNonZero pins the input-validation
// contract: omitting --id is a usage error, exits non-zero, and prints a
// message naming the missing flag.
func TestTaskText_BinaryMissingIdFlagExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	seedTaskDB(t, cfgDir, 42, "present-row")

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "task-text")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when --id is omitted, got success\nout: %s", out)
	}
	if !strings.Contains(string(out), "--id") {
		t.Errorf("stderr missing '--id' reference: %s", out)
	}
}
