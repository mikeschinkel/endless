package monitor

import (
	"database/sql"
	"fmt"
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
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id)
		 VALUES (42, 1, 'probe', 'now', 'in_progress', 1)`,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return db
}

// TestDisplayPath exercises the cwd-relative / ~ / absolute fallback
// chain. We swap the package-var displayPath's reads of cwd and HOME
// by relying on os.Chdir + a HOME env override.
func TestDisplayPath(t *testing.T) {
	prevHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", prevHome) })
	prevCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevCwd) })

	// t.TempDir() returns /var/... on macOS; os.Getwd() resolves the
	// /private/var/... symlink. Canonicalize so the prefix match in
	// displayPath sees both sides agree.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("evalsymlinks home: %v", err)
	}
	cwd := filepath.Join(home, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("setenv HOME: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"descendant of cwd", filepath.Join(cwd, ".endless", "worktrees", "e-1396"), filepath.Join(".endless", "worktrees", "e-1396")},
		{"descendant of HOME but not cwd", filepath.Join(home, "other", "dir"), filepath.Join("~", "other", "dir")},
		{"outside cwd and HOME", "/var/log/system.log", "/var/log/system.log"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayPath(tc.in); got != tc.want {
				t.Errorf("displayPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
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

// reaperFixture seeds the common state for E-1549 multi-signal-guard
// tests: a single task_landings row at landedAt and an installed
// runGit/hasLiveProcessInDir pair that records its calls and answers
// according to the supplied callbacks. The returned dir is created
// (so lsof has a real target) but contains no .git.
type reaperFixture struct {
	db       *sql.DB
	dir      string
	projRoot string
	calls    []string

	// answers — left as defaults to make the worktree look fully reapable.
	revListOut    string // "0" → no unmerged commits
	statusOut     string // "" → clean
	worktreeRmOut string // stub output for the "worktree" branch (e.g. git's stderr)
	revListErr    error
	statusErr     error
	worktreeRmErr error
	branchDelErr  error
	live          bool
}

func newReaperFixture(t *testing.T, landedAt time.Time) *reaperFixture {
	t.Helper()
	f := &reaperFixture{
		db:         newReaperTestDB(t),
		dir:        t.TempDir(),
		projRoot:   t.TempDir(),
		revListOut: "0",
		statusOut:  "",
	}
	if _, err := f.db.Exec(
		`INSERT INTO task_landings (task_id, branch, merge_commit_sha, landed_at)
		 VALUES (42, 'task/42-probe', 'deadbeef', ?)`,
		landedAt.UTC().Format("2006-01-02T15:04:05"),
	); err != nil {
		t.Fatalf("seed landing: %v", err)
	}

	prevRunGit := runGit
	prevLive := hasLiveProcessInDir
	runGit = func(dir string, args ...string) (string, error) {
		key := args[0]
		f.calls = append(f.calls, key)
		switch key {
		case "rev-list":
			return f.revListOut, f.revListErr
		case "status":
			return f.statusOut, f.statusErr
		case "worktree":
			return f.worktreeRmOut, f.worktreeRmErr
		case "branch":
			return "", f.branchDelErr
		}
		return "", nil
	}
	hasLiveProcessInDir = func(string) (bool, error) { return f.live, nil }
	t.Cleanup(func() {
		runGit = prevRunGit
		hasLiveProcessInDir = prevLive
	})
	return f
}

// TestMaybeReapWorktree_ReapsCleanAbandoned covers the happy path:
// landed long ago, no session activity, clean tree, no unmerged
// commits, no active session, no live process → reaped.
func TestMaybeReapWorktree_ReapsCleanAbandoned(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reaped {
		t.Errorf("expected reap=true for clean abandoned worktree, got false")
	}
	// Sanity: the destructive git ops were attempted.
	var sawRemove, sawBranchDel bool
	for _, c := range f.calls {
		if c == "worktree" {
			sawRemove = true
		}
		if c == "branch" {
			sawBranchDel = true
		}
	}
	if !sawRemove || !sawBranchDel {
		t.Errorf("expected worktree remove + branch -D, got calls=%v", f.calls)
	}
}

// TestMaybeReapWorktree_SessionTaskActivityProtects covers the E-1549
// bug: landed >TTL ago, but session_tasks.updated_at is within TTL
// (reopened-after-landing). Reaper must leave the worktree alone.
func TestMaybeReapWorktree_SessionTaskActivityProtects(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	// Seed a recent session_tasks row (within TTL).
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	if _, err := f.db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state)
		 VALUES (7, 'sess-7', 1, 'ended')`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := f.db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (7, 42, ?, ?)`,
		now, now,
	); err != nil {
		t.Fatalf("seed session_tasks: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false when session_tasks.updated_at is within TTL, got true")
	}
}

// TestMaybeReapWorktree_OldSessionTaskActivityReaps confirms that
// session_tasks.updated_at also older than TTL doesn't save the
// worktree — post-grace abandonment still reaps.
func TestMaybeReapWorktree_OldSessionTaskActivityReaps(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05")
	if _, err := f.db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state)
		 VALUES (7, 'sess-7', 1, 'ended')`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := f.db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (7, 42, ?, ?)`,
		old, old,
	); err != nil {
		t.Fatalf("seed session_tasks: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reaped {
		t.Errorf("expected reap=true when both landed_at and session_tasks.updated_at are past TTL, got false")
	}
}

// TestMaybeReapWorktree_UnmergedCommitsProtect: a branch with commits
// not yet on main must not be reaped.
func TestMaybeReapWorktree_UnmergedCommitsProtect(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.revListOut = "3"
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false when branch has unmerged commits, got true")
	}
}

// TestMaybeReapWorktree_ActiveSessionProtects: a non-ended session
// pointing at the task via active_task_id blocks reap.
func TestMaybeReapWorktree_ActiveSessionProtects(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	if _, err := f.db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, active_task_id)
		 VALUES (9, 'sess-9', 1, 'working', 42)`,
	); err != nil {
		t.Fatalf("seed active session: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false when an active session is bound to the task, got true")
	}
}

// TestMaybeReapWorktree_EndedSessionDoesNotProtect: a session in
// state='ended' must not save the worktree (the bug's belt+suspenders
// guard fires only for state != 'ended').
func TestMaybeReapWorktree_EndedSessionDoesNotProtect(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	if _, err := f.db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, active_task_id)
		 VALUES (9, 'sess-9', 1, 'ended', 42)`,
	); err != nil {
		t.Fatalf("seed ended session: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reaped {
		t.Errorf("expected reap=true when only blocker is an ended session, got false")
	}
}

// TestMaybeReapWorktree_DirtyTreeProtects: uncommitted edits in the
// worktree block reap.
func TestMaybeReapWorktree_DirtyTreeProtects(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.statusOut = " M internal/monitor/reap_worktrees.go\n"
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false for dirty working tree, got true")
	}
}

// TestMaybeReapWorktree_GitStatusErrorProtects: when git itself fails,
// the reaper treats the worktree as in-use and skips. The plan calls
// for conservatism: don't destroy a worktree we can't inspect.
func TestMaybeReapWorktree_GitStatusErrorProtects(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.statusErr = fmt.Errorf("fatal: not a git repository")
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false when git status errors, got true")
	}
}

// TestMaybeReapWorktree_GitRevListErrorProtects: same conservatism
// applies to the unmerged-commits probe.
func TestMaybeReapWorktree_GitRevListErrorProtects(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.revListErr = fmt.Errorf("fatal: ambiguous argument 'main'")
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false when git rev-list errors, got true")
	}
}

// TestMaybeReapWorktree_StrandedLeftover_Reaped covers E-1575: when
// `git worktree remove` aborts with "is not a working tree" (the dir
// exists on disk but git's worktree admin doesn't know it — leftover
// from a prior reap), the reaper treats it as benign, rmdir's the
// empty dir, skips branch -D, and reports the dir as reaped.
func TestMaybeReapWorktree_StrandedLeftover_Reaped(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.worktreeRmErr = fmt.Errorf("exit status 128")
	f.worktreeRmOut = "fatal: '" + f.dir + "' is not a working tree\n"
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reaped {
		t.Errorf("expected reap=true for stranded leftover, got false")
	}
	if _, statErr := os.Stat(f.dir); !os.IsNotExist(statErr) {
		t.Errorf("expected stranded dir to be rmdir'd, stat err=%v", statErr)
	}
	for _, c := range f.calls {
		if c == "branch" {
			t.Errorf("expected branch -D to be SKIPPED on stranded path, got calls=%v", f.calls)
		}
	}
}

// TestMaybeReapWorktree_StrandedLeftover_NonEmptyDirSurvives confirms
// the rmdir step uses os.Remove (not RemoveAll): if the stranded dir
// somehow has contents (user's leftover files), the rmdir fails
// harmlessly and the dir stays put. The reap is still treated as
// successful (no loud error on subsequent sweeps).
func TestMaybeReapWorktree_StrandedLeftover_NonEmptyDirSurvives(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.worktreeRmErr = fmt.Errorf("exit status 128")
	f.worktreeRmOut = "fatal: '" + f.dir + "' is not a working tree\n"
	// Plant a file so os.Remove on the dir fails (non-empty).
	planted := filepath.Join(f.dir, "user-leftover.txt")
	if err := os.WriteFile(planted, []byte("important"), 0o644); err != nil {
		t.Fatalf("plant file: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reaped {
		t.Errorf("expected reap=true for stranded leftover, got false")
	}
	if _, statErr := os.Stat(planted); statErr != nil {
		t.Errorf("expected planted file to survive (non-empty dir not removed), stat err=%v", statErr)
	}
}

// TestMaybeReapWorktree_GitWorktreeRemoveOtherErrorSurfaces confirms
// that errors from `git worktree remove` OTHER than "is not a working
// tree" still surface as before — the stranded-leftover branch is
// scoped tightly to that one message.
func TestMaybeReapWorktree_GitWorktreeRemoveOtherErrorSurfaces(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.worktreeRmErr = fmt.Errorf("exit status 128")
	f.worktreeRmOut = "fatal: working tree contains modified or untracked files\n"
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err == nil {
		t.Errorf("expected error to surface for non-stranded git failure")
	}
	if reaped {
		t.Errorf("expected reap=false when git worktree remove fails for an unrecognized reason")
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
