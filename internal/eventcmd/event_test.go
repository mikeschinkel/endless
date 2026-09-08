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
	"time"

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
		Type:      "todo",
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

// seedRebuildLossFixture builds the state a `rebuild-db --confirm` would
// destroy: a project, a task, a session BOUND to that task (the binding
// ED-1560 says is write-once), a landing row, a gate hanging off the task as
// its epic, and a judgment plus a label hanging off that gate.
//
// The gate's children are the point of the fixture. They are reached on the
// SECOND cascade hop — tasks -> session_gates -> report_judgments/report_labels
// — so a guard that walked only the direct children of `tasks` would report
// them as zero while the DELETE still took them.
func seedRebuildLossFixture(t *testing.T, dbPath, projectName string, taskID int64) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db for seed: %v", err)
	}
	defer db.Close()

	stmts := []struct {
		q    string
		args []any
	}{
		{"INSERT INTO projects (id, name, path) VALUES (1, ?, ?)",
			[]any{projectName, "/tmp/" + projectName}},
		{`INSERT INTO tasks (id, project_id, title, phase, status, type_id, sort_order)
		  VALUES (?, 1, 'bound task', 'now', 'ready', 1, 10)`, []any{taskID}},
		{`INSERT INTO sessions (id, session_id, project_id, task_id)
		  VALUES (1, 'ES-TEST-1', 1, ?)`, []any{taskID}},
		{`INSERT INTO task_landings (id, task_id, session_id, merge_commit_sha)
		  VALUES (1, ?, 1, 'deadbeef')`, []any{taskID}},
		{`INSERT INTO session_gates (id, session_id, kind_id, epic_id)
		  VALUES (1, 1, 1, ?)`, []any{taskID}},
		{`INSERT INTO report_judgments (id, gate_id) VALUES (1, 1)`, nil},
		{`INSERT INTO report_labels (id, gate_id, session_id, token)
		  VALUES (1, 1, 1, '$GOOD')`, nil},
	}
	for _, s := range stmts {
		if _, err := db.Exec(s.q, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.q, err)
		}
	}
}

// rebuildLossCounts snapshots exactly the numbers the refusal claims are at
// risk, so a test can assert the refusal wrote NOTHING by comparing the map
// before and after.
func rebuildLossCounts(t *testing.T, dbPath string) map[string]int64 {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db for counts: %v", err)
	}
	defer db.Close()

	queries := map[string]string{
		"tasks":            "SELECT count(*) FROM tasks",
		"task_landings":    "SELECT count(*) FROM task_landings",
		"session_gates":    "SELECT count(*) FROM session_gates",
		"report_judgments": "SELECT count(*) FROM report_judgments",
		"report_labels":    "SELECT count(*) FROM report_labels",
		"session_bindings": "SELECT count(*) FROM sessions WHERE task_id IS NOT NULL",
	}
	got := make(map[string]int64, len(queries))
	for name, q := range queries {
		var n int64
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		got[name] = n
	}
	return got
}

// TestEventRebuildDB_ConfirmRefusesAndDestroysNothing pins E-2062: --confirm is
// refused deliberately, it names what it would have destroyed with counts read
// from the live database, and it leaves every one of those counts untouched.
//
// This test replaces one that pinned the opposite contract ("--confirm replaces
// the tasks table"). That path was never reachable on a real database — the
// write-once trigger aborted it — and making it reachable would have destroyed
// the four tables seeded here. The refusal is the contract now; repairing the
// rebuild is E-799.
func TestEventRebuildDB_ConfirmRefusesAndDestroysNothing(t *testing.T) {
	cfgDir := t.TempDir()
	projectRoot := t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)

	const projectName = "proj-rebuild-confirm"
	const taskID int64 = 9001

	seedRebuildLossFixture(t, dbPath, projectName, taskID)

	// A projected task that does NOT exist live. If the guard ever leaks, this
	// id appears in tasks and the assertion below catches it.
	const projectedTaskID int64 = 9002
	writeLedgerEvent(t, projectRoot,
		makeTaskCreatedEvent(t, projectName, projectedTaskID, "confirm path task"))

	before := rebuildLossCounts(t, dbPath)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "rebuild-db",
		"--project-root", projectRoot,
		"--confirm",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("rebuild-db --confirm exited 0; want a refusal\nout: %s", out)
	}

	for _, want := range []string{
		"rebuild-db --confirm is disabled",
		"1 task_landings rows",
		"1 session_gates rows",
		"and 1 report_judgments, 1 report_labels",
		"1 sessions bindings",
		"task_deps",
		"E-799",
		"ED-1560",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("refusal does not mention %q; got:\n%s", want, out)
		}
	}

	// The projection must never have been built: the guard runs before it.
	if bytes.Contains(out, []byte("Projection:")) {
		t.Errorf("refusal built the projection first; want it refused up front:\n%s", out)
	}

	after := rebuildLossCounts(t, dbPath)
	for name, wantN := range before {
		if after[name] != wantN {
			t.Errorf("%s count changed across the refusal: %d -> %d", name, wantN, after[name])
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM tasks WHERE id = ?", projectedTaskID).Scan(&n); err != nil {
		t.Fatalf("count projected task: %v", err)
	}
	if n != 0 {
		t.Errorf("projected task %d was inserted; the refusal must write nothing", projectedTaskID)
	}
	var boundTask sql.NullInt64
	if err := db.QueryRow("SELECT task_id FROM sessions WHERE id = 1").Scan(&boundTask); err != nil {
		t.Fatalf("read session binding: %v", err)
	}
	if !boundTask.Valid || boundTask.Int64 != taskID {
		t.Errorf("sessions.task_id = %v, want %d — ED-1560 says it is never cleared",
			boundTask, taskID)
	}
}

// TestEventRebuildDB_ConfirmRefusesWithNoBoundSession covers the case the
// accidental fuse MISSES entirely. The write-once trigger aborts only when some
// session is bound to a task; a project with landings and gates but no live
// binding would sail straight through the DELETE and lose them silently. The
// guard is about what the command would destroy, not about whether the trigger
// happens to catch it.
func TestEventRebuildDB_ConfirmRefusesWithNoBoundSession(t *testing.T) {
	cfgDir := t.TempDir()
	projectRoot := t.TempDir()
	dbPath := initSchemaDB(t, cfgDir)

	const projectName = "proj-rebuild-unbound"
	const taskID int64 = 9101

	seedTaskRow(t, dbPath, projectName, taskID, "unbound task")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db for seed: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO task_landings (id, task_id, merge_commit_sha) VALUES (1, ?, 'cafe')`,
		taskID,
	); err != nil {
		db.Close()
		t.Fatalf("seed landing: %v", err)
	}
	db.Close()

	writeLedgerEvent(t, projectRoot,
		makeTaskCreatedEvent(t, projectName, taskID, "unbound task"))

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "rebuild-db",
		"--project-root", projectRoot,
		"--confirm",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("rebuild-db --confirm exited 0 on an unbound project; want a refusal\nout: %s", out)
	}
	if !bytes.Contains(out, []byte("rebuild-db --confirm is disabled")) {
		t.Errorf("expected the refusal, got:\n%s", out)
	}
	if !bytes.Contains(out, []byte("0 sessions bindings")) {
		t.Errorf("expected the binding count to read 0, got:\n%s", out)
	}
	if !bytes.Contains(out, []byte("1 task_landings rows")) {
		t.Errorf("expected the landing that would have been lost, got:\n%s", out)
	}

	after := rebuildLossCounts(t, dbPath)
	if after["task_landings"] != 1 {
		t.Errorf("task_landings = %d after the refusal, want 1", after["task_landings"])
	}
}

// TestEventRebuildDB_ConfirmRefusesBeforeReadingTheLedger proves the guard runs
// FIRST. With no ledger at all the projection fails loudly ("no events found"),
// so seeing the refusal instead — and not that error — is direct evidence that
// nothing ran before it.
func TestEventRebuildDB_ConfirmRefusesBeforeReadingTheLedger(t *testing.T) {
	cfgDir := t.TempDir()
	projectRoot := t.TempDir()
	initSchemaDB(t, cfgDir)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"event", "rebuild-db",
		"--project-root", projectRoot,
		"--confirm",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("rebuild-db --confirm exited 0 with no ledger; want a refusal\nout: %s", out)
	}
	if !bytes.Contains(out, []byte("rebuild-db --confirm is disabled")) {
		t.Errorf("expected the refusal, got:\n%s", out)
	}
	if bytes.Contains(out, []byte("no events found")) {
		t.Errorf("the projector ran before the guard; want the guard first:\n%s", out)
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

// TestEventBackup_ReportsTheDestinationPath pins E-1942's contract: stdout
// carries the file that was written, and it is the file that is actually on
// disk. `endless db backup` prints that path, so "backed up" stops being a
// claim the user has to go and verify by hand before a restore.
func TestEventBackup_ReportsTheDestinationPath(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	bin := endlessGoBin(t)
	out, err := exec.Command(bin, "--config-dir", cfgDir, "event", "backup").Output()
	if err != nil {
		t.Fatalf("backup failed: %v\nstdout: %s", err, out)
	}
	var result map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &result); err != nil {
		t.Fatalf("decode output: %v\nraw: %s", err, out)
	}
	if got := result["status"]; got != "ok" {
		t.Fatalf("expected status=ok, got %v; raw: %s", got, out)
	}
	path, _ := result["path"].(string)
	if path == "" {
		t.Fatalf("expected a path in the output; raw: %s", out)
	}
	if dir := filepath.Dir(path); dir != filepath.Join(cfgDir, "backups") {
		t.Errorf("backup landed outside <cfgDir>/backups: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("reported path does not exist: %v", err)
	}
}

// TestEventBackup_SkippedReportsTheExistingBackup covers the throttle: a second
// call inside the 60s window writes nothing, and must say so rather than
// reporting a path it did not write. Reporting "ok" here would have the CLI
// claim a fresh backup that does not exist.
func TestEventBackup_SkippedReportsTheExistingBackup(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	bin := endlessGoBin(t)
	first, err := exec.Command(bin, "--config-dir", cfgDir, "event", "backup").Output()
	if err != nil {
		t.Fatalf("first backup failed: %v\nstdout: %s", err, first)
	}
	second, err := exec.Command(bin, "--config-dir", cfgDir, "event", "backup").Output()
	if err != nil {
		t.Fatalf("second backup failed: %v\nstdout: %s", err, second)
	}

	var one, two map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(first), &one); err != nil {
		t.Fatalf("decode first: %v\nraw: %s", err, first)
	}
	if err := json.Unmarshal(bytes.TrimSpace(second), &two); err != nil {
		t.Fatalf("decode second: %v\nraw: %s", err, second)
	}
	if got := two["status"]; got != "skipped" {
		t.Errorf("expected status=skipped inside the throttle window, got %v; raw: %s",
			got, second)
	}
	if two["path"] != one["path"] {
		t.Errorf("skipped run should name the existing backup %v, got %v",
			one["path"], two["path"])
	}
}

// TestEventBackup_NoDatabaseFailsLoudly: there is nothing to back up, so
// claiming success would be a lie the user only discovers when they need the
// backup. Exits non-zero and names the path it looked for.
func TestEventBackup_NoDatabaseFailsLoudly(t *testing.T) {
	cfgDir := t.TempDir()

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir, "event", "backup")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		t.Fatalf("expected a non-zero exit with no database; stdout: %s", out)
	}
	if !strings.Contains(stderr.String(), "no database at") {
		t.Errorf("expected the missing-database path to be named, got: %s", stderr.String())
	}
}

// TestEventBackup_EnforcesTieredRetention is E-2121 end to end through the real
// binary: writing a backup also prunes the directory BY AGE.
//
// The three seeded backups are, respectively, aged out of every tier, inside the
// weekly tier, and a same-hour duplicate of one already there. Only the first
// and the third may go, and nothing that is not one of BackupDB's own filenames
// may be touched at all — the rotation this replaced deleted by list position,
// so a stray file that sorted early went first.
func TestEventBackup_EnforcesTieredRetention(t *testing.T) {
	cfgDir := t.TempDir()
	initSchemaDB(t, cfgDir)

	backupDir := filepath.Join(cfgDir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	seed := func(name string) string {
		path := filepath.Join(backupDir, name)
		if err := os.WriteFile(path, []byte("not really a database"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return path
	}
	name := func(t time.Time) string {
		return "endless-" + t.Format("20060102-150405") + ".db"
	}
	stamp := func(age time.Duration) string {
		return name(time.Now().Add(-age))
	}

	expired := seed(stamp(400 * 24 * time.Hour))
	keptWeekly := seed(stamp(200 * 24 * time.Hour))
	// Two backups in one calendar day, six days back: same daily bucket, so
	// the older one loses.
	//
	// Anchored to that day's midnight rather than offset back from now. Ages
	// pick the TIER, but buckets are calendar periods, so a pair expressed as
	// "six days back" and "six days and ninety minutes back" shares a daily
	// bucket only when the suite runs after 01:30 — before that the older one
	// falls into the previous day, wins its own bucket, and survives a sweep
	// the test says should drop it. It failed exactly that way at 00:44.
	sixDaysBack := time.Now().Add(-6 * 24 * time.Hour)
	dayStart := time.Date(
		sixDaysBack.Year(), sixDaysBack.Month(), sixDaysBack.Day(),
		0, 0, 0, 0, time.Local,
	)
	olderInBucket := seed(name(dayStart.Add(10 * time.Hour)))
	newerInBucket := seed(name(dayStart.Add(11*time.Hour + 30*time.Minute)))
	foreign := seed("operators-copy.sqlite")

	bin := endlessGoBin(t)
	out, err := exec.Command(bin, "--config-dir", cfgDir, "event", "backup").Output()
	if err != nil {
		t.Fatalf("backup failed: %v\nstdout: %s", err, out)
	}
	var result map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &result); err != nil {
		t.Fatalf("decode output: %v\nraw: %s", err, out)
	}
	if got, ok := result["pruned"].(float64); !ok || int(got) != 2 {
		t.Errorf("pruned = %v, want 2; raw: %s", result["pruned"], out)
	}
	if warning := result["warning"]; warning != nil {
		t.Errorf("unexpected retention warning: %v", warning)
	}

	for _, gone := range []string{expired, olderInBucket} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived the retention sweep", filepath.Base(gone))
		}
	}
	for _, kept := range []string{keptWeekly, newerInBucket, foreign} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed but should have been kept: %v", filepath.Base(kept), err)
		}
	}
	// And the backup this run wrote is on disk under the path it reported.
	path, _ := result["path"].(string)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("reported path does not exist: %v", err)
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
