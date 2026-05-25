package monitor

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

func newReaperTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("set fks: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-05-16T00:00:00', '2026-05-16T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, phase, status, type)
		 VALUES (42, 1, 'probe', 'now', 'in_progress', 'task')`,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return db
}

func TestParseWorktreeTTL(t *testing.T) {
	tests := []struct {
		in    string
		want  time.Duration
		isErr bool
	}{
		{"14d", 14 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"3600s", 3600 * time.Second, false},
		{"7d12h", 7*24*time.Hour + 12*time.Hour, false},
		{"0d", 0, false},
		{"  14d  ", 14 * 24 * time.Hour, false},
		{"", 0, true},
		{"   ", 0, true},
		{"forever", 0, true},
		{"14days", 0, true}, // strict: only single "d"
		{"d", 0, true},      // no digits before d
	}
	for _, tt := range tests {
		got, err := ParseWorktreeTTL(tt.in)
		if tt.isErr {
			if err == nil {
				t.Errorf("ParseWorktreeTTL(%q): expected error, got %v", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseWorktreeTTL(%q): unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseWorktreeTTL(%q): got %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestReadWorktreeTTLConfig_Missing(t *testing.T) {
	// Non-existent directory must return "" rather than erroring — the
	// reaper falls back to DefaultWorktreeTTL on any config-read miss.
	if got := ReadWorktreeTTLConfig(t.TempDir()); got != "" {
		t.Errorf("expected empty string for project without config.json, got %q", got)
	}
}

func TestReadWorktreeTTLConfig_PresentField(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".endless"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(proj, ".endless", "config.json"),
		[]byte(`{"name":"test","worktree_ttl":"7d"}`),
		0o644,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if got := ReadWorktreeTTLConfig(proj); got != "7d" {
		t.Errorf("got %q, want %q", got, "7d")
	}
}

// TestMaybeReapWorktree_NoLandingRow asserts that a worktree directory
// whose owning task has no rows in task_landings is treated as a
// pre-existing orphan and skipped, regardless of TTL.
func TestMaybeReapWorktree_NoLandingRow(t *testing.T) {
	db := newReaperTestDB(t)
	// No task_landings rows for task 42.
	cutoff := time.Now().UTC().Add(-time.Hour)
	reaped, err := maybeReapWorktree(db, t.TempDir(), "/tmp/fake-worktree-dir", 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false for task with no landing row, got true")
	}
}

// TestMaybeReapWorktree_LandingTooRecent asserts that a worktree whose
// most-recent landing is younger than the cutoff is skipped.
func TestMaybeReapWorktree_LandingTooRecent(t *testing.T) {
	db := newReaperTestDB(t)
	// Insert a landing row with timestamp = now (well after any reasonable cutoff).
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	if _, err := db.Exec(
		`INSERT INTO task_landings (task_id, branch, merge_commit_sha, landed_at)
		 VALUES (42, 'task/42-probe', 'deadbeef', ?)`,
		now,
	); err != nil {
		t.Fatalf("seed landing: %v", err)
	}
	// Cutoff is 14 days ago — anything newer than this is "too recent".
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(db, t.TempDir(), "/tmp/fake-worktree-dir", 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false for landing < ttl old, got true")
	}
}

// TestReapStaleWorktrees_NoWorktreesDir asserts the reaper silently
// no-ops when .endless/worktrees doesn't exist (fresh project, no
// worktrees ever created).
func TestReapStaleWorktrees_NoWorktreesDir(t *testing.T) {
	proj := t.TempDir() // empty: no .endless/worktrees subtree
	if err := ReapStaleWorktrees(proj, 14*24*time.Hour); err != nil {
		t.Errorf("expected nil error for missing worktrees dir, got %v", err)
	}
}

// TestReapStaleWorktrees_SkipsNonMatchingDirNames asserts that dirs
// that don't fit the e-NNNN naming pattern are silently skipped
// (no panic, no errant SQL queries for unparseable IDs).
func TestReapStaleWorktrees_SkipsNonMatchingDirNames(t *testing.T) {
	// ReapStaleWorktrees opens DB() once it finds worktree dirs. The test
	// binary's cwd is inside this self-dev worktree, so the E-1429 gate
	// refuses unless an explicit DB context is set — provide an isolated
	// throwaway, as production satisfies the gate via --db.
	dbContextDir = ""
	SetDBContextDir(t.TempDir())
	t.Cleanup(func() { dbContextDir = "" })

	proj := t.TempDir()
	wtroot := filepath.Join(proj, ".endless", "worktrees")
	for _, name := range []string{"e-abc", "not-a-task", "e-", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(wtroot, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if err := ReapStaleWorktrees(proj, 14*24*time.Hour); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	// All four dirs should still exist (none matched and none were reaped).
	for _, name := range []string{"e-abc", "not-a-task", "e-", ".hidden"} {
		if _, err := os.Stat(filepath.Join(wtroot, name)); err != nil {
			t.Errorf("dir %s should still exist: %v", name, err)
		}
	}
}
