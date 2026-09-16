package sessionquerycmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// seedReportDB writes a schema-applied DB with a project, a focal task, a
// follow-up that cleans_up the focal, and a child of the focal.
func seedReportDB(t *testing.T, cfgDir string) {
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
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, 'p', '/tmp/p')",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	stmts := []string{
		// type_id 1 = 'todo' (see schema.sql's task_types seed) — the focal task
		// is deliberately NOT an epic, which is what the Python renderer gates
		// the Children list on (E-1880).
		"INSERT INTO tasks (id, project_id, title, status, type_id) VALUES (10, 1, 'focal', 'unverified', 1)",
		"INSERT INTO tasks (id, project_id, title, status) VALUES (11, 1, 'follow-up', 'submitted')",
		"INSERT INTO tasks (id, project_id, title, status, parent_id) VALUES (12, 1, 'child', 'assumed', 10)",
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', 11, 'task', 10, 'cleans_up')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed row (%s): %v", s, err)
		}
	}
}

// TestTaskReport_BinaryEmitsFacts pins the session-query task-report contract:
// the binary prints the focal task's computed facts as JSON — status, type, its
// cleaned_up_by successor, and its child — and exits 0. `type` is part of the
// wire contract because the Python renderer renders Children for epics only
// (E-1880); dropping it would silently turn every report into a non-epic one.
func TestTaskReport_BinaryEmitsFacts(t *testing.T) {
	cfgDir := t.TempDir()
	seedReportDB(t, cfgDir)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--db-dir", cfgDir,
		"session-query", "task-report", "--id", "10")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary exec failed: %v\nout: %s", err, out)
	}

	var facts monitor.TaskReportFacts
	if err := json.Unmarshal(out, &facts); err != nil {
		t.Fatalf("unmarshal facts: %v\nout: %s", err, out)
	}
	if facts.Status != "unverified" {
		t.Errorf("status = %q, want unverified", facts.Status)
	}
	if facts.Type != "todo" {
		t.Errorf("type = %q, want todo", facts.Type)
	}
	// The raw JSON must carry the key, not just the Go struct: the Python
	// renderer reads facts["type"] out of this payload.
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal raw facts: %v\nout: %s", err, out)
	}
	if raw["type"] != "todo" {
		t.Errorf("JSON key \"type\" = %v, want todo", raw["type"])
	}
	if len(facts.Successors) != 1 || facts.Successors[0].ID != 11 ||
		facts.Successors[0].Relation != "cleaned_up_by" {
		t.Errorf("successors = %+v, want one cleaned_up_by E-11", facts.Successors)
	}
	// E-1911: children are off the wire entirely. The seed gives E-10 a child
	// (E-12) precisely so their absence here is a real assertion — `session
	// status` is where a task's children belong, and the report duplicating
	// them is what listed two non-children under E-1785.
	if _, ok := raw["children"]; ok {
		t.Errorf("facts JSON still carries a children key: %s", out)
	}
	// Asserted against the DECODED payload, not a substring of it. A bare
	// strings.Contains(out, "12") matched any "12" anywhere — a timestamp, a
	// row count, and (E-1668) a temp path in the provenance field — so it
	// could fail without the child being present and pass while it was, if the
	// id were spelled across a line break. The property is "no key of this
	// payload names E-12", so read the keys.
	for key, value := range raw {
		// task_id IS 10 and may legitimately contain the digits; keys prefixed
		// with "_" are metadata ABOUT the answer rather than facts in it, and
		// one of them carries a t.TempDir() path whose random digits contain
		// "12" roughly one run in ten. That is what made the old
		// strings.Contains(out, "12") flaky rather than wrong-once.
		if key == "task_id" || strings.HasPrefix(key, "_") {
			continue
		}
		if strings.Contains(fmt.Sprint(value), "12") {
			t.Errorf("facts JSON still names the child E-12 under %q: %s", key, out)
		}
	}
}

// TestTaskReport_BinaryMissingIdExitsNonZero pins the input-validation contract.
func TestTaskReport_BinaryMissingIdExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	seedReportDB(t, cfgDir)
	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--db-dir", cfgDir, "session-query", "task-report")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected non-zero exit when --id omitted\nout: %s", out)
	}
}
