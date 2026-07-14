package sessionquerycmd

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
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
		"INSERT INTO tasks (id, project_id, title, status) VALUES (10, 1, 'focal', 'unverified')",
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
// the binary prints the focal task's computed facts as JSON — status, its
// cleaned_up_by successor, and its child — and exits 0.
func TestTaskReport_BinaryEmitsFacts(t *testing.T) {
	cfgDir := t.TempDir()
	seedReportDB(t, cfgDir)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
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
	if len(facts.Successors) != 1 || facts.Successors[0].ID != 11 ||
		facts.Successors[0].Relation != "cleaned_up_by" {
		t.Errorf("successors = %+v, want one cleaned_up_by E-11", facts.Successors)
	}
	if len(facts.Children) != 1 || facts.Children[0].ID != 12 {
		t.Errorf("children = %+v, want one child E-12", facts.Children)
	}
}

// TestTaskReport_BinaryMissingIdExitsNonZero pins the input-validation contract.
func TestTaskReport_BinaryMissingIdExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	seedReportDB(t, cfgDir)
	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir, "session-query", "task-report")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected non-zero exit when --id omitted\nout: %s", out)
	}
}
