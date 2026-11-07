package correctcmd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// withProject stands up a schema-applied database bound to monitor.DB(), a
// registered project whose root is a git repository with one commit, and a
// task in it. It returns the database, the project root and the task id.
func withProject(t *testing.T) (*sql.DB, string, int64) {
	t.Helper()
	// Keep the process out of any sandbox or real config: ConfigDir() decides
	// whether the ledger is committed, and HOME decides which config that is.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// A person, not an agent, whatever runs the tests.
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	t.Setenv("__CFBundleIdentifier", "")

	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@example.com",
			"-c", "user.name=T", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "--allow-empty", "-q", "-m", "initial")

	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := schema.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', ?, 'active', '2026-05-30T00:00:00', '2026-05-30T00:00:00')`, root); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(
		`INSERT INTO tasks (project_id, title, phase, status, type_id, sort_order, created_at)
		 VALUES (1, 'Landed wrongly', 'now', 'landed', 1, 10, '2026-05-30T00:00:00')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	t.Cleanup(monitor.SetTestDB(db))
	return db, root, id
}

func baseRequest(root string, id int64) request {
	return request{
		target: "task", key: "E-" + itoa(id), reason: "the land skipped a gate",
		project: "test", projectRoot: root, nodeID: "a1b2", actorKind: "cli", actorID: "tester",
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func gitLog(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "log", "--format=%s%n%b").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestRun_RefusesAnAgent(t *testing.T) {
	db, root, id := withProject(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	r := baseRequest(root, id)
	r.sets = pairList{"status=assumed"}
	r.skipGates = true
	err := run(r, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "the user's command") {
		t.Fatalf("want the agent refusal, got %v", err)
	}
	var status string
	_ = db.QueryRow("SELECT status FROM tasks WHERE id = ?", id).Scan(&status)
	if status != "landed" {
		t.Errorf("an agent changed the row: %s", status)
	}
	if _, err := os.Stat(filepath.Join(root, ".endless", events.LedgerDirName)); err == nil {
		t.Errorf("an agent wrote to the ledger")
	}
}

func TestRun_CorrectsAndCommitsAdjacentEntries(t *testing.T) {
	db, root, id := withProject(t)
	r := baseRequest(root, id)
	r.sets = pairList{"status=assumed"}
	r.skipGates = true
	var out bytes.Buffer
	if err := run(r, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	var status string
	_ = db.QueryRow("SELECT status FROM tasks WHERE id = ?", id).Scan(&status)
	if status != "assumed" {
		t.Errorf("status = %q", status)
	}
	var result struct {
		Kind    string  `json:"kind"`
		Segment string  `json:"segment"`
		Refs    []int64 `json:"refs"`
		Summary string  `json:"summary"`
		Op      string  `json:"op"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	if result.Kind != "ledger.corrected" || result.Op != "update" || len(result.Refs) != 1 {
		t.Errorf("result = %+v", result)
	}
	if !strings.Contains(result.Summary, "status landed → assumed") {
		t.Errorf("summary = %q", result.Summary)
	}
	// The segment holds the change entry at the recorded offset, and the
	// correction right after it.
	data, err := os.ReadFile(filepath.Join(root, ".endless", events.LedgerDirName, result.Segment))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("segment lines = %d: %q", len(lines), data)
	}
	var change, corrected events.Event
	if err := json.Unmarshal(data[result.Refs[0]:], &change); err == nil && change.Kind != events.KindLedgerRowChanged {
		t.Errorf("offset %d holds %s", result.Refs[0], change.Kind)
	}
	if err := json.Unmarshal([]byte(lines[1]), &corrected); err != nil || corrected.Kind != events.KindLedgerCorrected {
		t.Errorf("second line: %v %s", err, corrected.Kind)
	}
	var p events.CorrectedPayload
	_ = json.Unmarshal(corrected.Payload, &p)
	if p.Refs[0] != result.Refs[0] || p.Before[0].Values["status"] != "landed" || !p.SkipGates {
		t.Errorf("corrected payload = %+v", p)
	}
	// One ledger commit, whose subject names the correction and the entity.
	log := gitLog(t, root)
	first := strings.SplitN(log, "\n", 2)[0]
	if !strings.HasPrefix(first, "Endless: ledger.corrected E-"+itoa(id)) {
		t.Errorf("subject = %q", first)
	}
	if !strings.Contains(log, "ledger.row_changed E-"+itoa(id)+" update task") {
		t.Errorf("the body lists the change entry:\n%s", log)
	}

	// A later ordinary event does not fold into the correction's commit, and a
	// second correction does not fold into the first.
	r.sets = pairList{"title=Landed rightly"}
	r.reason = "typo in the title"
	r.skipGates = false
	if err := run(r, &bytes.Buffer{}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	subjects := strings.Split(gitLog(t, root), "\n")
	var corrections int
	for _, s := range subjects {
		if strings.HasPrefix(s, "Endless: ledger.corrected") {
			corrections++
		}
	}
	if corrections != 2 {
		t.Errorf("want two correction commits, got %d:\n%s", corrections, gitLog(t, root))
	}
}

func TestRun_GateRefusalWritesNothing(t *testing.T) {
	_, root, id := withProject(t)
	r := baseRequest(root, id)
	r.sets = pairList{"status=assumed"}
	err := run(r, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "gate refuses") {
		t.Fatalf("want a gate refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".endless", events.LedgerDirName)); err == nil {
		t.Errorf("a refused correction reached the ledger")
	}
	if !strings.Contains(gitLog(t, root), "initial") || strings.Contains(gitLog(t, root), "Endless:") {
		t.Errorf("a refused correction was committed")
	}
}

func TestRun_FlagShapes(t *testing.T) {
	_, root, id := withProject(t)
	r := baseRequest(root, id)
	r.sets = pairList{"status=assumed"}
	r.del = true
	if err := run(r, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Errorf("set+delete: %v", err)
	}
	r = baseRequest(root, id)
	r.sets = pairList{"status"}
	if err := run(r, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "col=value") {
		t.Errorf("bare column: %v", err)
	}
	r = baseRequest(root, id)
	r.sets = pairList{"title=a", "title=b"}
	if err := run(r, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate column: %v", err)
	}
	r = baseRequest(root, id)
	r.reason = ""
	if err := run(r, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Errorf("no reason: %v", err)
	}
}
