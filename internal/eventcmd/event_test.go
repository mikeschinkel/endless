// Package eventcmd binary-integration tests (E-1506).
//
// These tests build cmd/endless-go once via TestMain, then exercise each
// `endless-go event <subcommand>` from outside-in: argv parsing, the
// dispatcher's gate (E-1429 worktree DB context via --config-dir), and
// the subcommand's documented contract (exit code, stdout shape, side
// effects). Per-test t.TempDir() is the config dir so each test starts
// from a fresh DB.
//
// Synthetic JSONL ledger entries are constructed from struct literals
// here in the test file rather than from on-disk fixtures: each test
// emits exactly the events it needs, keeping the test self-contained and
// avoiding testdata/ drift.
package eventcmd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/schema"
)

// endlessGoBinPath holds the path to the endless-go binary that
// TestMain built once for the package's whole test sweep. Per-test
// t.TempDir() would be cleaned up between tests; TestMain owns the
// directory for the lifetime of `go test`.
var endlessGoBinPath string

// TestMain builds endless-go once before the suite, mirroring the
// sessionquerycmd pattern. The build cost is amortized across every
// binary-integration test in this package.
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

// initSchemaDB writes a schema-applied endless.db at cfgDir/endless.db
// and returns its path. Each test gets its own cfgDir.
func initSchemaDB(t *testing.T, cfgDir string) string {
	t.Helper()
	dbPath := filepath.Join(cfgDir, "endless.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return dbPath
}

// seedTaskRow inserts a tasks row matching the synthetic task.created
// event below so ValidateTasks finds it in the current DB.
func seedTaskRow(t *testing.T, dbPath string, projectName string, taskID int64, title string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db for seed: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, ?, ?)",
		projectName, "/tmp/"+projectName,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id, sort_order)
		 VALUES (?, 1, ?, 'now', 'ready', 1, 10)`,
		taskID, title,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

// writeLedgerEvent serializes one event to a single-line JSONL segment in
// projectRoot/.endless/db-ledger/. Mirrors what events.Writer.Append
// would produce, but avoids triggering the auto-commit path so tests do
// not need a git repo.
func writeLedgerEvent(t *testing.T, projectRoot string, evt events.Event) {
	t.Helper()
	ledgerDir := filepath.Join(projectRoot, ".endless", events.LedgerDirName)
	if err := os.MkdirAll(ledgerDir, 0755); err != nil {
		t.Fatalf("mkdir ledger dir: %v", err)
	}
	// Reader scans by prefix/suffix; node hex + sequence are otherwise free.
	segPath := filepath.Join(ledgerDir,
		events.LedgerFilePrefix+"a7f3-000001"+events.LedgerFileSuffix)
	f, err := os.OpenFile(segPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	defer f.Close()
	line, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatalf("write segment line: %v", err)
	}
}

// makeTaskCreatedEvent builds a synthetic task.created event with the
// minimum fields ProjectToTempDB's replayTaskCreated requires (a valid
// kairos ts, a phase, status, type, and a numeric entity id).
func makeTaskCreatedEvent(t *testing.T, projectName string, taskID int64, title string) events.Event {
	t.Helper()
	// Node id 0xa7f3 = the prefix the segment filename uses; the projector
	// does not enforce a match, but keep them aligned for readability.
	clock := kairos.NewClock(0xa7f3)
	ts := clock.Now().String()

	payload, err := json.Marshal(events.TaskCreatedPayload{
		Title:     title,
		Phase:     "now",
		Status:    "ready",
		Type:      "task",
		SortOrder: 10,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	return events.Event{
		V:       events.Version,
		TS:      ts,
		Kind:    events.KindTaskCreated,
		Project: projectName,
		Entity: events.EntityRef{
			Type: events.EntityTask,
			ID:   fmt.Sprintf("%d", taskID),
		},
		Actor: events.Actor{
			Kind: events.ActorCLI,
			ID:   "test",
		},
		Payload: payload,
	}
}

// TestEventEmit_MissingKindExitsNonZero pins the input-validation
// contract of `event emit`: omitting --kind is a usage error, exits
// non-zero, and stderr names the missing flag. This covers the
// dispatcher → runEmit → run() flag-validation branch without needing
// the full git-repo + ledger-writer happy path.
func TestEventEmit_MissingKindExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "emit",
		"--project", "proj",
		"--entity-type", "task",
		"--actor-kind", "cli",
		"--actor-id", "test",
		"--node-id", "a7f3",
		"--project-root", cfgDir,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when --kind omitted, got success\nout: %s", out)
	}
	if !bytes.Contains(out, []byte("--kind")) {
		t.Errorf("stderr missing '--kind' reference: %s", out)
	}
}

// TestEventEmit_UnknownKindExitsNonZero pins the closed-vocabulary
// guard: events.ValidKinds rejects an unrecognized kind before any
// ledger write, so the binary errors with a "unknown event kind"
// message and exits non-zero. Locks in the contract that emit cannot
// silently write an arbitrary kind string.
func TestEventEmit_UnknownKindExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "emit",
		"--kind", "definitely.not.a.kind",
		"--project", "proj",
		"--entity-type", "task",
		"--entity-id", "1",
		"--actor-kind", "cli",
		"--actor-id", "test",
		"--node-id", "a7f3",
		"--project-root", cfgDir,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for unknown kind, got success\nout: %s", out)
	}
	if !bytes.Contains(out, []byte("unknown event kind")) {
		t.Errorf("stderr missing 'unknown event kind' reference: %s", out)
	}
}

// TestEventValidateDB_HappyPathReportsMatch pins the green-path contract
// of `event validate-db`: one synthetic task.created event in the ledger
// plus a matching tasks row in the current DB produces an "All projected
// tasks match" line on stdout and exit 0. This covers ReadAllEvents →
// ProjectToTempDB → ValidateTasks end-to-end through the binary.
func TestEventValidateDB_HappyPathReportsMatch(t *testing.T) {
	cfgDir := t.TempDir()
	projectRoot := t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)

	const projectName = "proj-validate"
	const taskID int64 = 4242
	const title = "validate-db happy path"

	evt := makeTaskCreatedEvent(t, projectName, taskID, title)
	writeLedgerEvent(t, projectRoot, evt)
	seedTaskRow(t, dbPath, projectName, taskID, title)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "validate-db",
		"--project-root", projectRoot,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validate-db failed: %v\nout: %s", err, out)
	}
	if !bytes.Contains(out, []byte("1 events replayed")) {
		t.Errorf("expected '1 events replayed' in output, got: %s", out)
	}
	if !bytes.Contains(out, []byte("All projected tasks match current DB state")) {
		t.Errorf("expected match-line in output, got: %s", out)
	}
}

// TestEventValidateDB_MissingProjectRootExitsNonZero pins the usage-error
// contract: omitting --project-root exits non-zero with a stderr
// explanation.
func TestEventValidateDB_MissingProjectRootExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "validate-db",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when --project-root omitted, got success\nout: %s", out)
	}
	if !bytes.Contains(out, []byte("--project-root")) {
		t.Errorf("stderr missing '--project-root' reference: %s", out)
	}
}

// TestEventRebuildDB_DryRunReportsProjectedCounts pins the default
// (non-confirm) behavior of `event rebuild-db`: one synthetic
// task.created event in the ledger is projected, the run prints the
// "Dry run" banner, and the tasks table is NOT modified.
func TestEventRebuildDB_DryRunReportsProjectedCounts(t *testing.T) {
	cfgDir := t.TempDir()
	projectRoot := t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)

	const projectName = "proj-rebuild-dry"
	const taskID int64 = 8001

	evt := makeTaskCreatedEvent(t, projectName, taskID, "dry run task")
	writeLedgerEvent(t, projectRoot, evt)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "rebuild-db",
		"--project-root", projectRoot,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rebuild-db dry run failed: %v\nout: %s", err, out)
	}
	if !bytes.Contains(out, []byte("Dry run")) {
		t.Errorf("expected 'Dry run' banner in output, got: %s", out)
	}
	if !bytes.Contains(out, []byte("1 tasks created")) {
		t.Errorf("expected '1 tasks created' in projection summary, got: %s", out)
	}
	// Confirm the tasks table was NOT modified (dry run).
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM tasks WHERE id = ?", taskID).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if n != 0 {
		t.Errorf("dry-run should not insert into tasks; found %d row(s) for id %d", n, taskID)
	}
}

// TestEventRebuildDB_ConfirmReplacesTasksTable pins the destructive
// path: with --confirm, the projected tasks land in the current DB's
// tasks table. The pre-existing project row stays; only its tasks are
// replaced.
func TestEventRebuildDB_ConfirmReplacesTasksTable(t *testing.T) {
	cfgDir := t.TempDir()
	projectRoot := t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)

	const projectName = "proj-rebuild-confirm"
	const taskID int64 = 9001

	// Seed the project row in current DB so the DELETE-by-name in
	// rebuild-db has a project to target. We don't seed the task — the
	// rebuild is what should insert it.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, ?, ?)",
		projectName, "/tmp/"+projectName,
	); err != nil {
		db.Close()
		t.Fatalf("seed project: %v", err)
	}
	db.Close()

	evt := makeTaskCreatedEvent(t, projectName, taskID, "confirm path task")
	writeLedgerEvent(t, projectRoot, evt)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "rebuild-db",
		"--project-root", projectRoot,
		"--confirm",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rebuild-db --confirm failed: %v\nout: %s", err, out)
	}
	if !bytes.Contains(out, []byte("Rebuilt: tasks table replaced")) {
		t.Errorf("expected 'Rebuilt: tasks table replaced' line, got: %s", out)
	}
	// Confirm the projected task is now in the current DB.
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db2.Close()
	var n int
	if err := db2.QueryRow("SELECT count(*) FROM tasks WHERE id = ?", taskID).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row for projected task id %d, got %d", taskID, n)
	}
}

// TestEventApplyChange_NoopSQLRecordsMarker pins the .sql happy path:
// applying a no-op SQL change records a _schema_version marker and
// prints status=applied JSON to stdout.
func TestEventApplyChange_NoopSQLRecordsMarker(t *testing.T) {
	cfgDir := t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)

	// Write a no-op .sql change file. Statement must be valid SQL the
	// schema accepts; SELECT 1 is harmless. The basename (sans ext) is
	// the marker name.
	changeDir := t.TempDir()
	changePath := filepath.Join(changeDir, "test-noop-change.sql")
	if err := os.WriteFile(changePath, []byte("SELECT 1;\n"), 0644); err != nil {
		t.Fatalf("write change file: %v", err)
	}

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "apply-change", changePath,
	)
	// Use Output so log lines on stderr don't pollute the JSON parse.
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("apply-change failed: %v\nstdout: %s", err, out)
	}
	var result map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &result); err != nil {
		t.Fatalf("decode apply-change output: %v\nraw: %s", err, out)
	}
	if got := result["status"]; got != "applied" {
		t.Errorf("expected status=applied, got %v; raw: %s", got, out)
	}
	if got := result["name"]; got != "test-noop-change" {
		t.Errorf("expected name=test-noop-change, got %v", got)
	}
	// Confirm the marker is recorded.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM _schema_version WHERE name = ?",
		"test-noop-change",
	).Scan(&n); err != nil {
		t.Fatalf("query _schema_version: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 marker row for test-noop-change, got %d", n)
	}
}

// TestEventApplyChange_AlreadyAppliedIsSkipped pins the idempotency
// contract: a second apply of the same change file reports status=skipped
// without re-executing.
func TestEventApplyChange_AlreadyAppliedIsSkipped(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	changeDir := t.TempDir()
	changePath := filepath.Join(changeDir, "test-idempotent.sql")
	if err := os.WriteFile(changePath, []byte("SELECT 1;\n"), 0644); err != nil {
		t.Fatalf("write change file: %v", err)
	}

	bin := endlessGoBin(t)
	// First run: applied.
	first := exec.Command(bin, "--config-dir", cfgDir,
		"event", "apply-change", changePath,
	)
	if out, err := first.Output(); err != nil {
		t.Fatalf("first apply-change failed: %v\nstdout: %s", err, out)
	}
	// Second run: should report skipped.
	second := exec.Command(bin, "--config-dir", cfgDir,
		"event", "apply-change", changePath,
	)
	out, err := second.Output()
	if err != nil {
		t.Fatalf("second apply-change failed: %v\nstdout: %s", err, out)
	}
	var result map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &result); err != nil {
		t.Fatalf("decode second-run output: %v\nraw: %s", err, out)
	}
	if got := result["status"]; got != "skipped" {
		t.Errorf("expected status=skipped on re-apply, got %v; raw: %s", got, out)
	}
}

// TestEventBackup_WritesBackupFileUnderConfigDir pins the documented
// side-effect of `event backup`: it calls monitor.BackupDB which writes
// a VACUUMed copy of the DB into <cfgDir>/backups/. With a fresh DB and
// no prior backups, the resulting dir must contain at least one .db
// file and stdout must be {"status":"ok"}.
func TestEventBackup_WritesBackupFileUnderConfigDir(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir, "event", "backup")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("backup failed: %v\nstdout: %s", err, out)
	}
	if !strings.Contains(string(out), `"status":"ok"`) {
		t.Errorf("expected status ok json, got: %s", out)
	}

	backupDir := filepath.Join(cfgDir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backups dir: %v", err)
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".db") {
			count++
		}
	}
	if count < 1 {
		t.Errorf("expected at least one .db backup under %s, got %d entries: %v",
			backupDir, len(entries), entries)
	}
}

// TestEvent_UnknownSubcommandExitsNonZero pins the dispatcher's
// unknown-subcommand branch: an unrecognized verb after `event` exits
// non-zero with a stderr message naming it.
func TestEvent_UnknownSubcommandExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "not-a-real-subcommand",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for unknown subcommand, got success\nout: %s", out)
	}
	if !bytes.Contains(out, []byte("Unknown command")) {
		t.Errorf("stderr missing 'Unknown command' marker: %s", out)
	}
}
