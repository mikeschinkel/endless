package sandboxcmd

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/schema"
)

// initTestMainCheckoutWithWorktree creates a main checkout (git repo) with a
// linked worktree under .endless/worktrees/e-NNN, plus a main DB at the
// returned path with the given project row. Returns (mainCheckout, worktree,
// mainDBPath).
func initTestMainCheckoutWithWorktree(t *testing.T, projectName string) (string, string, string) {
	t.Helper()

	rawRoot := t.TempDir()
	// Canonicalize macOS /var → /private/var so paths match what
	// git rev-parse --git-common-dir produces (git resolves symlinks).
	root, err := filepath.EvalSymlinks(rawRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	mainCheckout := filepath.Join(root, "project")
	if err := os.MkdirAll(mainCheckout, 0o755); err != nil {
		t.Fatalf("mkdir main checkout: %v", err)
	}
	gitOrFatal(t, mainCheckout, "init", "-q", "-b", "main")
	gitOrFatal(t, mainCheckout, "config", "user.email", "test@example.com")
	gitOrFatal(t, mainCheckout, "config", "user.name", "test")
	gitOrFatal(t, mainCheckout, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(mainCheckout, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	gitOrFatal(t, mainCheckout, "add", "README")
	gitOrFatal(t, mainCheckout, "commit", "-q", "-m", "init")

	worktree := filepath.Join(mainCheckout, ".endless", "worktrees", "e-9999")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatalf("mkdir worktrees dir: %v", err)
	}
	gitOrFatal(t, mainCheckout, "worktree", "add", "-q", "-b", "task/9999", worktree)

	mainDBPath := filepath.Join(root, "main-config", "endless.db")
	if err := os.MkdirAll(filepath.Dir(mainDBPath), 0o755); err != nil {
		t.Fatalf("mkdir main config: %v", err)
	}
	db, err := sql.Open("sqlite", mainDBPath)
	if err != nil {
		t.Fatalf("open main DB: %v", err)
	}
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	_, err = db.Exec(
		"INSERT INTO projects (name, label, path, status, language, created_at, updated_at) "+
			"VALUES (?, ?, ?, 'active', 'go', '2026-01-01T00:00:00', '2026-01-01T00:00:00')",
		projectName, "Test "+projectName, mainCheckout,
	)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	db.Close()

	return mainCheckout, worktree, mainDBPath
}

// withChdir cd's to dir for the test's duration.
func withChdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		os.Chdir(orig)
	})
}

// withHomeAndSessionEnv overrides HOME so seedFromWorktree's main-DB lookup
// finds our test fixture, and sets/unsets CLAUDE_CODE_SESSION_ID.
func withHomeAndSessionEnv(t *testing.T, home, sessionID string) {
	t.Helper()
	t.Setenv("HOME", home)
	if sessionID == "" {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "")
		os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	} else {
		t.Setenv("CLAUDE_CODE_SESSION_ID", sessionID)
	}
}

func TestSeedFromWorktree_CopiesProjectAndSeedsSessionFromEnv(t *testing.T) {
	mainCheckout, worktree, mainDBPath := initTestMainCheckoutWithWorktree(t, "test-proj")
	home := filepath.Dir(filepath.Dir(mainDBPath)) // root/
	// readMainProjectRow expects ~/.config/endless/endless.db
	cfgEndless := filepath.Join(home, ".config", "endless")
	if err := os.MkdirAll(cfgEndless, 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	if err := os.Rename(mainDBPath, filepath.Join(cfgEndless, "endless.db")); err != nil {
		t.Fatalf("rename db: %v", err)
	}

	withHomeAndSessionEnv(t, home, "claude-sess-abc123")
	withChdir(t, worktree)

	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("mkdir sandbox: %v", err)
	}

	if err := seedFromWorktree(sandboxDir); err != nil {
		t.Fatalf("seedFromWorktree: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(sandboxDir, "endless", "endless.db"))
	if err != nil {
		t.Fatalf("open sandbox DB: %v", err)
	}
	defer db.Close()

	var name, path string
	if err := db.QueryRow("SELECT name, path FROM projects").Scan(&name, &path); err != nil {
		t.Fatalf("query projects: %v", err)
	}
	if name != "test-proj" {
		t.Errorf("project name = %q, want test-proj", name)
	}
	if path != mainCheckout {
		t.Errorf("project path = %q, want %q (main checkout)", path, mainCheckout)
	}

	var sid string
	var projID int64
	if err := db.QueryRow("SELECT session_id, project_id FROM sessions").Scan(&sid, &projID); err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	if sid != "claude-sess-abc123" {
		t.Errorf("session_id = %q, want claude-sess-abc123", sid)
	}
	if projID == 0 {
		t.Errorf("project_id = 0, want non-zero FK to projects.id")
	}
}

func TestSeedFromWorktree_WritesConfigJSON(t *testing.T) {
	_, worktree, mainDBPath := initTestMainCheckoutWithWorktree(t, "test-proj-cfg")
	home := filepath.Dir(filepath.Dir(mainDBPath))
	cfgEndless := filepath.Join(home, ".config", "endless")
	os.MkdirAll(cfgEndless, 0o755)
	os.Rename(mainDBPath, filepath.Join(cfgEndless, "endless.db"))

	withHomeAndSessionEnv(t, home, "claude-sess-cfg")
	withChdir(t, worktree)

	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	os.MkdirAll(sandboxDir, 0o755)

	if err := seedFromWorktree(sandboxDir); err != nil {
		t.Fatalf("seedFromWorktree: %v", err)
	}

	configPath := filepath.Join(sandboxDir, "endless", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read sandbox config.json: %v", err)
	}
	var cfg sandboxConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config.json is not valid JSON: %v\n%s", err, data)
	}
	if len(cfg.Roots) != 1 || cfg.Roots[0] != "~/Projects" {
		t.Errorf("roots = %v, want [~/Projects]", cfg.Roots)
	}
	if cfg.ScanInterval != 300 {
		t.Errorf("scan_interval = %d, want 300", cfg.ScanInterval)
	}
	if cfg.Ignore == nil {
		t.Errorf("ignore = nil, want [] (must serialize as a JSON array)")
	}
}

func TestSeedFromWorktree_FallsBackToNullSessionID(t *testing.T) {
	_, worktree, mainDBPath := initTestMainCheckoutWithWorktree(t, "test-proj-2")
	home := filepath.Dir(filepath.Dir(mainDBPath))
	cfgEndless := filepath.Join(home, ".config", "endless")
	os.MkdirAll(cfgEndless, 0o755)
	os.Rename(mainDBPath, filepath.Join(cfgEndless, "endless.db"))

	withHomeAndSessionEnv(t, home, "") // empty → unset
	withChdir(t, worktree)

	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	os.MkdirAll(sandboxDir, 0o755)

	if err := seedFromWorktree(sandboxDir); err != nil {
		t.Fatalf("seedFromWorktree: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(sandboxDir, "endless", "endless.db"))
	if err != nil {
		t.Fatalf("open sandbox DB: %v", err)
	}
	defer db.Close()

	var sid string
	if err := db.QueryRow("SELECT session_id FROM sessions").Scan(&sid); err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	if sid != nullSessionID {
		t.Errorf("session_id = %q, want %q", sid, nullSessionID)
	}
}

func TestSeedFromWorktree_ErrorsWhenNoMatchingProject(t *testing.T) {
	_, worktree, mainDBPath := initTestMainCheckoutWithWorktree(t, "wrong-name")
	home := filepath.Dir(filepath.Dir(mainDBPath))
	cfgEndless := filepath.Join(home, ".config", "endless")
	os.MkdirAll(cfgEndless, 0o755)
	// Replace main DB with one that has a different project path so the
	// lookup-by-path fails.
	otherDB := filepath.Join(cfgEndless, "endless.db")
	os.Remove(otherDB)
	db, err := sql.Open("sqlite", otherDB)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	_, err = db.Exec(
		"INSERT INTO projects (name, path, status, created_at, updated_at) "+
			"VALUES ('other', '/nowhere', 'active', '2026-01-01T00:00:00', '2026-01-01T00:00:00')",
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	db.Close()
	os.Remove(mainDBPath) // remove stub from fixture path

	withHomeAndSessionEnv(t, home, "")
	withChdir(t, worktree)

	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	os.MkdirAll(sandboxDir, 0o755)
	err = seedFromWorktree(sandboxDir)
	if err == nil {
		t.Fatal("expected error when main DB has no matching project row")
	}
	if !strings.Contains(err.Error(), "no project in main DB at path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSeedFromWorktree_ErrorsWhenNotInWorktree(t *testing.T) {
	mainCheckout, _, mainDBPath := initTestMainCheckoutWithWorktree(t, "test-proj-3")
	home := filepath.Dir(filepath.Dir(mainDBPath))

	withHomeAndSessionEnv(t, home, "")
	withChdir(t, mainCheckout) // main checkout, not the worktree

	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	os.MkdirAll(sandboxDir, 0o755)
	err := seedFromWorktree(sandboxDir)
	if err == nil {
		t.Fatal("expected error when cwd is the main checkout")
	}
	if !strings.Contains(err.Error(), "is the main checkout") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReadSeededSessionID_MissingDBNoError(t *testing.T) {
	sandboxDir := t.TempDir()
	sid, err := readSeededSessionID(sandboxDir)
	if err != nil {
		t.Fatalf("expected no error for missing DB, got: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty session_id, got %q", sid)
	}
}

func TestReadSeededSessionID_NoRowsNoError(t *testing.T) {
	sandboxDir := t.TempDir()
	endlessDir := filepath.Join(sandboxDir, "endless")
	os.MkdirAll(endlessDir, 0o755)
	db, err := sql.Open("sqlite", filepath.Join(endlessDir, "endless.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	db.Close()

	sid, err := readSeededSessionID(sandboxDir)
	if err != nil {
		t.Fatalf("expected no error for empty sessions table, got: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty session_id, got %q", sid)
	}
}

func TestGoWrapperBody_IncludesSessionExportWhenSet(t *testing.T) {
	body := goWrapperBody("/sb/dir", "/bin/endless-go", "sess-xyz")
	if !strings.Contains(body, "export ENDLESS_SESSION_ID='sess-xyz'") {
		t.Errorf("expected ENDLESS_SESSION_ID export, got:\n%s", body)
	}
	if !strings.Contains(body, "export XDG_CONFIG_HOME='/sb/dir'") {
		t.Errorf("expected XDG_CONFIG_HOME export, got:\n%s", body)
	}
}

func TestGoWrapperBody_OmitsSessionExportWhenEmpty(t *testing.T) {
	body := goWrapperBody("/sb/dir", "/bin/endless-go", "")
	if strings.Contains(body, "ENDLESS_SESSION_ID") {
		t.Errorf("expected no ENDLESS_SESSION_ID export, got:\n%s", body)
	}
}
