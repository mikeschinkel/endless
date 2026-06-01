package sessionquerycmd

import (
	"database/sql"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// seedEnsureClaudeIDDB writes an endless.db at $cfgDir/endless.db with the
// schema applied, one project at the given path, and any pre-existing
// sessions the test wants. Used by the ensure-claude-id binary tests.
func seedEnsureClaudeIDDB(t *testing.T, cfgDir, projectPath string, sessions []sessionSeed) {
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
		"INSERT INTO projects (id, name, path) VALUES (1, 'proj-seed', ?)",
		projectPath,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	for _, s := range sessions {
		if _, err := db.Exec(
			`INSERT INTO sessions (session_id, project_id, platform, state, process, last_activity)
			 VALUES (?, 1, 'claude', ?, ?, '2026-05-20T00:00:00')`,
			s.sessionID, s.state, s.process,
		); err != nil {
			t.Fatalf("seed session %s: %v", s.sessionID, err)
		}
	}
}

func readSessionsRow(t *testing.T, cfgDir, sessionID string) (id int64, state, process string, projectID int64) {
	t.Helper()
	dbPath := filepath.Join(cfgDir, "endless.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var p sql.NullString
	var pid sql.NullInt64
	if err := db.QueryRow(
		"SELECT id, state, COALESCE(process,''), COALESCE(project_id,0) FROM sessions WHERE session_id = ?",
		sessionID,
	).Scan(&id, &state, &p, &pid); err != nil {
		t.Fatalf("read session row %q: %v", sessionID, err)
	}
	return id, state, p.String, pid.Int64
}

// TestEnsureClaudeID_ReturnsExistingRow pins the steady-state path: the
// session row already exists (hook has fired previously). The verb prints
// that row's integer id and does not duplicate the row.
func TestEnsureClaudeID_ReturnsExistingRow(t *testing.T) {
	cfgDir := t.TempDir()
	projectPath := t.TempDir()
	seedEnsureClaudeIDDB(t, cfgDir, projectPath, []sessionSeed{
		{"existing-uuid", "working", "%5"},
	})
	// Capture the seeded row's id for comparison.
	wantID, _, _, _ := readSessionsRow(t, cfgDir, "existing-uuid")

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "ensure-claude-id",
		"--session-id", "existing-uuid",
		"--project-root", projectPath,
		"--process", "%5")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary exec failed: %v\nstdout: %s", err, out)
	}
	got, parseErr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if parseErr != nil {
		t.Fatalf("parse id from %q: %v", out, parseErr)
	}
	if got != wantID {
		t.Errorf("got id %d, want %d", got, wantID)
	}
}

// TestEnsureClaudeID_LazyCreatesMissingRow pins the first-event-timing
// case: no hook has fired yet and the DB has no row for the env-identified
// Claude session. The verb INSERTs a 'needs_input' row keyed to the
// session_id and prints the new row's id.
func TestEnsureClaudeID_LazyCreatesMissingRow(t *testing.T) {
	cfgDir := t.TempDir()
	projectPath := t.TempDir()
	seedEnsureClaudeIDDB(t, cfgDir, projectPath, nil)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "ensure-claude-id",
		"--session-id", "fresh-uuid",
		"--project-root", projectPath,
		"--process", "%9")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary exec failed: %v\nstdout: %s", err, out)
	}
	got, parseErr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if parseErr != nil {
		t.Fatalf("parse id from %q: %v", out, parseErr)
	}
	id, state, process, projectID := readSessionsRow(t, cfgDir, "fresh-uuid")
	if id != got {
		t.Errorf("printed id %d does not match row id %d", got, id)
	}
	if state != "needs_input" {
		t.Errorf("state = %q, want 'needs_input' (TouchSession's INSERT default)", state)
	}
	if process != "%9" {
		t.Errorf("process = %q, want '%%9'", process)
	}
	if projectID != 1 {
		t.Errorf("project_id = %d, want 1", projectID)
	}
}

// TestEnsureClaudeID_IdempotentSecondCall pins concurrency safety: a
// second call with the same session_id returns the same id (no duplicate
// row, no error). The Python resolver may fire this lazily from multiple
// CLI invocations in the same race window.
func TestEnsureClaudeID_IdempotentSecondCall(t *testing.T) {
	cfgDir := t.TempDir()
	projectPath := t.TempDir()
	seedEnsureClaudeIDDB(t, cfgDir, projectPath, nil)

	bin := endlessGoBin(t)
	run := func() int64 {
		t.Helper()
		cmd := exec.Command(bin, "--config-dir", cfgDir,
			"session-query", "ensure-claude-id",
			"--session-id", "idempotent-uuid",
			"--project-root", projectPath)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("binary exec failed: %v\nstdout: %s", err, out)
		}
		id, parseErr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if parseErr != nil {
			t.Fatalf("parse id from %q: %v", out, parseErr)
		}
		return id
	}

	first := run()
	second := run()
	if first != second {
		t.Errorf("non-idempotent: first=%d, second=%d", first, second)
	}
}

// TestEnsureClaudeID_MissingSessionIDFlag pins input validation: omitting
// --session-id is a usage error.
func TestEnsureClaudeID_MissingSessionIDFlag(t *testing.T) {
	cfgDir := t.TempDir()
	projectPath := t.TempDir()
	seedEnsureClaudeIDDB(t, cfgDir, projectPath, nil)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "ensure-claude-id",
		"--project-root", projectPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when --session-id is omitted, got success\nout: %s", out)
	}
	if !strings.Contains(string(out), "--session-id") {
		t.Errorf("stderr missing '--session-id' reference: %s", out)
	}
}

// TestEnsureClaudeID_MissingProjectRootFlag pins input validation:
// omitting --project-root is a usage error. The helper needs a project
// to attach the lazy-created row to; "no project context" is a caller
// error, not something the verb should guess at.
func TestEnsureClaudeID_MissingProjectRootFlag(t *testing.T) {
	cfgDir := t.TempDir()
	projectPath := t.TempDir()
	seedEnsureClaudeIDDB(t, cfgDir, projectPath, nil)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "ensure-claude-id",
		"--session-id", "fresh-uuid")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when --project-root is omitted, got success\nout: %s", out)
	}
	if !strings.Contains(string(out), "--project-root") {
		t.Errorf("stderr missing '--project-root' reference: %s", out)
	}
}
