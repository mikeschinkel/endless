package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		 VALUES (42, 1, 'probe', 'now', 'underway', 1)`,
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

// TestMaybeReapWorktree_NoLandingRow asserts that a task with no rows in
// task_landings AND no session activity is skipped: E-2087 dropped the landing
// requirement, but a directory nothing was ever recorded about still has no
// moment to measure the TTL against.
func TestMaybeReapWorktree_NoLandingRow(t *testing.T) {
	db := newReaperTestDB(t)
	// No task_landings and no session_tasks rows for task 42.
	cutoff := time.Now().UTC().Add(-time.Hour)
	reaped, err := maybeReapWorktree(db, t.TempDir(), "/tmp/fake-worktree-dir", 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false for task with nothing recorded, got true")
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
	revListOut    string // "0" → the branch is contained in the base
	statusOut     string // "" → clean
	worktreeRmOut string // stub output for the "worktree" branch (e.g. git's stderr)
	revListErr    error
	statusErr     error
	worktreeRmErr error
	branchDelErr  error
	live          bool

	// unlanded is how many commits the content comparison finds no counterpart
	// for on the base — condition 4's actual verdict since E-2087. revListOut
	// only decides whether the comparison is reached at all.
	unlanded     int
	rangeDiffErr error
	// branchOut answers `symbolic-ref --short HEAD`, which is where the branch
	// name comes from when no landing row recorded one.
	branchOut string
	// revListArgs records condition 4's arguments so a test can assert which
	// revisions it excluded.
	revListArgs []string

	// revParseErr makes every branch candidate fail to resolve, which is how
	// DefaultBranch reports "I cannot name this repo's default branch" (E-1940).
	revParseErr error
}

func newReaperFixture(t *testing.T, landedAt time.Time) *reaperFixture {
	t.Helper()
	projRoot := t.TempDir()
	// Place the worktree dir at a real <projRoot>/.endless/worktrees/e-42
	// (task ID 42 is seeded below) so the stranded-orphan path exercises
	// removeStrandedWorktreeDir's real path guard. Tests that never reach
	// that path are unaffected by where dir lives.
	dir := filepath.Join(projRoot, ".endless", "worktrees", "e-42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir worktree dir: %v", err)
	}
	f := &reaperFixture{
		db:         newReaperTestDB(t),
		dir:        dir,
		projRoot:   projRoot,
		revListOut: "0",
		statusOut:  "",
		branchOut:  "task/42-probe",
	}
	if !landedAt.IsZero() {
		if _, err := f.db.Exec(
			`INSERT INTO task_landings (task_id, branch, merge_commit_sha, landed_at)
			 VALUES (42, 'task/42-probe', 'deadbeef', ?)`,
			landedAt.UTC().Format("2006-01-02T15:04:05"),
		); err != nil {
			t.Fatalf("seed landing: %v", err)
		}
	}

	prevRunGit := runGit
	prevLive := hasLiveProcessInDir
	runGit = func(dir string, args ...string) (string, error) {
		key := args[0]
		f.calls = append(f.calls, key)
		switch key {
		case "rev-list":
			f.revListArgs = append([]string{}, args...)
			return f.revListOut, f.revListErr
		case "merge-base":
			return "b45e0000\n", nil
		case "range-diff":
			if f.rangeDiffErr != nil {
				return "", f.rangeDiffErr
			}
			return stubRangeDiff(f.unlanded), nil
		case "rev-parse":
			if f.revParseErr != nil {
				return "", f.revParseErr
			}
			return "deadbeef\n", nil
		case "status":
			return f.statusOut, f.statusErr
		case "symbolic-ref":
			// The default-branch resolver asks for origin/HEAD; condition 4's
			// enrichment asks for HEAD.
			if args[len(args)-1] == "refs/remotes/origin/HEAD" {
				return "", fmt.Errorf("not a symbolic ref")
			}
			return f.branchOut, nil
		case "worktree":
			return f.worktreeRmOut, f.worktreeRmErr
		case "branch":
			return "", f.branchDelErr
		}
		return "", nil
	}
	hasLiveProcessInDir = func(string) (bool, error) { return f.live, nil }
	// The reaper resolves the default branch per directory and memoizes it; a
	// fixture that rewrites what git answers must not read the previous test's
	// verdict.
	resetDefaultBranchCache()
	t.Cleanup(func() {
		runGit = prevRunGit
		hasLiveProcessInDir = prevLive
		resetDefaultBranchCache()
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

// TestMaybeReapWorktree_NullBranchSkipsBranchDelete covers E-1719: a
// record-only/historical landing records a NULL branch. The reaper must read
// that row without erroring (it scans branch as sql.NullString) and skip the
// `git branch -D` step, since there is no branch to delete.
func TestMaybeReapWorktree_NullBranchSkipsBranchDelete(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	if _, err := f.db.Exec("UPDATE task_landings SET branch = NULL WHERE task_id = 42"); err != nil {
		t.Fatalf("null out branch: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error reading NULL-branch landing: %v", err)
	}
	if !reaped {
		t.Errorf("expected reap=true for clean abandoned worktree with NULL branch, got false")
	}
	for _, c := range f.calls {
		if c == "branch" {
			t.Errorf("expected NO `git branch -D` for a NULL-branch landing, got calls=%v", f.calls)
		}
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
	f.unlanded = 3
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
// pointing at the task via task_id blocks reap.
func TestMaybeReapWorktree_ActiveSessionProtects(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	if _, err := f.db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, task_id)
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
		`INSERT INTO sessions (id, session_id, project_id, state, task_id)
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

// TestMaybeReapWorktree_ModifiedTreeProtects: uncommitted edits in the
// worktree block reap.
func TestMaybeReapWorktree_ModifiedTreeProtects(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.statusOut = " M internal/monitor/reap_worktrees.go\n"
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Errorf("expected reap=false for modified working tree, got true")
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

// TestMaybeReapWorktree_StrandedLeftover_Reaped covers E-1575/E-1745: when
// `git worktree remove` aborts with "is not a working tree" (the dir exists on
// disk but git's worktree admin doesn't know it — leftover from a prior reap),
// the reaper removes the (here empty) dir via the path-scoped RemoveAll, skips
// branch -D, and reports the dir as reaped.
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

// TestMaybeReapWorktree_StrandedLeftover_NonEmptyDirReaped covers E-1745: a
// NON-EMPTY stranded orphan (the real-world case — the persistent e-1281 etc.
// dirs the old os.Remove could never delete) is now fully removed via the
// path-scoped RemoveAll, and reaped=true means the dir is actually gone.
func TestMaybeReapWorktree_StrandedLeftover_NonEmptyDirReaped(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.worktreeRmErr = fmt.Errorf("exit status 128")
	f.worktreeRmOut = "fatal: '" + f.dir + "' is not a working tree\n"
	// Plant a file so the dir is non-empty (os.Remove would refuse it; the
	// fix's RemoveAll must still delete it).
	planted := filepath.Join(f.dir, "user-leftover.txt")
	if err := os.WriteFile(planted, []byte("stale"), 0o644); err != nil {
		t.Fatalf("plant file: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reaped {
		t.Errorf("expected reap=true for non-empty stranded leftover, got false")
	}
	if _, statErr := os.Stat(f.dir); !os.IsNotExist(statErr) {
		t.Errorf("expected non-empty stranded dir to be removed, stat err=%v", statErr)
	}
}

// TestRemoveStrandedWorktreeDir_RefusesOutsideRoot: a dir that is not under
// <projRoot>/.endless/worktrees must be refused (error, nothing deleted).
func TestRemoveStrandedWorktreeDir_RefusesOutsideRoot(t *testing.T) {
	projRoot := t.TempDir()
	// e-NNN basename, but a sibling of the worktrees root, not inside it.
	sentinel := filepath.Join(projRoot, ".endless", "e-42")
	if err := os.MkdirAll(sentinel, 0o755); err != nil {
		t.Fatalf("mkdir sentinel: %v", err)
	}
	if err := removeStrandedWorktreeDir(projRoot, sentinel); err == nil {
		t.Errorf("expected error removing dir outside worktrees root, got nil")
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Errorf("expected sentinel to survive refusal, stat err=%v", statErr)
	}
}

// TestRemoveStrandedWorktreeDir_RefusesNonMatchingBasename: a dir inside the
// worktrees root whose basename is not e-NNN must be refused.
func TestRemoveStrandedWorktreeDir_RefusesNonMatchingBasename(t *testing.T) {
	projRoot := t.TempDir()
	sentinel := filepath.Join(projRoot, ".endless", "worktrees", "not-a-task")
	if err := os.MkdirAll(sentinel, 0o755); err != nil {
		t.Fatalf("mkdir sentinel: %v", err)
	}
	if err := removeStrandedWorktreeDir(projRoot, sentinel); err == nil {
		t.Errorf("expected error removing non-e-NNN basename, got nil")
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Errorf("expected sentinel to survive refusal, stat err=%v", statErr)
	}
}

// TestRemoveStrandedWorktreeDir_RefusesSymlink: an e-NNN entry under the
// worktrees root that is itself a symlink must be refused, and its target must
// be left untouched (never follow a symlink to somewhere else).
func TestRemoveStrandedWorktreeDir_RefusesSymlink(t *testing.T) {
	projRoot := t.TempDir()
	wtRoot := filepath.Join(projRoot, ".endless", "worktrees")
	if err := os.MkdirAll(wtRoot, 0o755); err != nil {
		t.Fatalf("mkdir worktrees root: %v", err)
	}
	// Target dir with a file, living outside the worktrees root.
	target := filepath.Join(t.TempDir(), "real-data")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	planted := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(planted, []byte("precious"), 0o644); err != nil {
		t.Fatalf("plant file: %v", err)
	}
	link := filepath.Join(wtRoot, "e-42")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := removeStrandedWorktreeDir(projRoot, link); err == nil {
		t.Errorf("expected error removing symlinked e-NNN entry, got nil")
	}
	if _, statErr := os.Stat(planted); statErr != nil {
		t.Errorf("expected symlink target contents to survive, stat err=%v", statErr)
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

// TestMaybeReapWorktree_SkipsWhenDefaultBranchUnresolved pins the fail-closed
// half of E-1940 on the code path that DELETES worktrees. The resolver replaced
// a hardcoded `main` in condition 4, so it became a new way for that condition
// to fail — and the only acceptable answer to "I cannot tell whether this
// branch has unlanded work" is skip, never reap.
func TestMaybeReapWorktree_SkipsWhenDefaultBranchUnresolved(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	f.revParseErr = fmt.Errorf("fatal: unknown revision")
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)

	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Fatal("an unresolvable default branch must skip, never reap")
	}
	for _, c := range f.calls {
		if c == "worktree" || c == "branch" {
			t.Errorf("destructive git %q ran despite an unresolvable base (calls=%v)", c, f.calls)
		}
	}
}

// TestMaybeReapWorktree_UnrecordedRebaseLandingIsNotReaped pins a debt, not a
// feature. E-2087 briefly gave the reaper the exact content comparison, which
// recognised a rebase-landed branch with no task_landings row and reclaimed it.
// That probe costs ~1-2s per worktree and the reaper runs on PreToolUse and
// PostToolUse, so the sweep cost ~90s before and after every tool call in every
// session. It was reverted to the cheap containment test.
//
// The consequence, asserted here so it is visible rather than merely absent: a
// branch whose work reached the base under rewritten SHAs, with nothing
// recorded, is never reclaimed. Reversing this assertion is the point of
// E-2111 — do it there, with something that makes the exact answer affordable,
// not by loosening the test.
func TestMaybeReapWorktree_UnrecordedRebaseLandingIsNotReaped(t *testing.T) {
	f := newReaperFixture(t, time.Time{}) // no landing row to credit
	// Three commits ahead by SHA; every one of them is on the base under a
	// different hash, which only a content comparison could see.
	f.revListOut = "3"
	f.unlanded = 0
	if _, err := f.db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (1, 42, ?, ?)`,
		time.Now().Add(-30*24*time.Hour).UTC().Format("2006-01-02T15:04:05"),
		time.Now().Add(-30*24*time.Hour).UTC().Format("2006-01-02T15:04:05"),
	); err != nil {
		t.Fatalf("seed session activity: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)

	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Fatal("the reaper reached for the exact comparison again — see E-2111")
	}
	for _, c := range f.calls {
		if c == "range-diff" {
			t.Errorf("the reaper ran range-diff; it is on the PreToolUse/PostToolUse path (calls=%v)", f.calls)
		}
	}
}

// TestMaybeReapWorktree_CreditsRecordedLandings is E-1940's regression, kept
// after E-2087's revert: a rebasing land rewrites every SHA, so without
// crediting the recorded landing the cheap containment test can never clear a
// worktree that landed through `worktree land`, and landed worktrees accumulate
// without bound.
func TestMaybeReapWorktree_CreditsRecordedLandings(t *testing.T) {
	f := newReaperFixture(t, time.Now().Add(-30*24*time.Hour))
	if _, err := f.db.Exec(
		`INSERT INTO task_landings (task_id, branch, merge_commit_sha, landed_at)
		 VALUES (42, 'task/42-probe', 'cafebabe', '2026-01-01T00:00:00')`,
	); err != nil {
		t.Fatalf("seed second landing: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)

	if _, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	excluded := map[string]bool{}
	for _, a := range f.revListArgs {
		if strings.HasPrefix(a, "^") {
			excluded[a] = true
		}
	}
	for _, want := range []string{"^main", "^deadbeef", "^cafebabe"} {
		if !excluded[want] {
			t.Errorf("condition 4 did not exclude %s (args=%v)", want, f.revListArgs)
		}
	}
}

// TestMaybeReapWorktree_SettledWithNoLandingIsReapable is E-2087's eligibility
// change. The reaper used to require a task_landings row, which read as "only
// reclaim what has landed" but actually meant "only reclaim what was RECORDED
// as landing" — so a worktree whose branch sits at the base holding nothing was
// settled, disposable, and never reclaimed. E-1360 and E-1697 sat in exactly
// that state. What is required now is that there be nothing to land.
func TestMaybeReapWorktree_SettledWithNoLandingIsReapable(t *testing.T) {
	f := newReaperFixture(t, time.Time{}) // no landing row at all
	if _, err := f.db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (1, 42, ?, ?)`,
		time.Now().Add(-30*24*time.Hour).UTC().Format("2006-01-02T15:04:05"),
		time.Now().Add(-30*24*time.Hour).UTC().Format("2006-01-02T15:04:05"),
	); err != nil {
		t.Fatalf("seed session activity: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)

	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reaped {
		t.Fatalf("a settled worktree with nothing to land was not reaped (calls=%v)", f.calls)
	}
	// The branch still has to be deleted, and with no landing row the only
	// place its name can come from is git.
	var sawBranchDelete bool
	for _, c := range f.calls {
		if c == "branch" {
			sawBranchDelete = true
		}
	}
	if !sawBranchDelete {
		t.Errorf("branch was not deleted (calls=%v)", f.calls)
	}
}

// TestMaybeReapWorktree_NoLandingStillNeedsSomethingToLandNothing keeps the
// eligibility change honest in the other direction: dropping the landing
// requirement must not make an UNSETTLED worktree reapable just because nothing
// was ever recorded for it.
func TestMaybeReapWorktree_NoLandingStillProtectsUnlandedWork(t *testing.T) {
	f := newReaperFixture(t, time.Time{})
	f.revListOut = "2"
	f.unlanded = 2
	if _, err := f.db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (1, 42, ?, ?)`,
		time.Now().Add(-30*24*time.Hour).UTC().Format("2006-01-02T15:04:05"),
		time.Now().Add(-30*24*time.Hour).UTC().Format("2006-01-02T15:04:05"),
	); err != nil {
		t.Fatalf("seed session activity: %v", err)
	}
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)

	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Fatal("a worktree holding unlanded work was reaped because it had no landing row")
	}
}

// TestMaybeReapWorktree_NoRecordedActivityIsSkipped is the floor under the
// eligibility change. With neither a landing nor session activity there is no
// timestamp to age off, and a TTL measured against nothing would reap a
// directory created seconds ago.
func TestMaybeReapWorktree_NoRecordedActivityIsSkipped(t *testing.T) {
	f := newReaperFixture(t, time.Time{})
	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)

	reaped, err := maybeReapWorktree(f.db, f.projRoot, f.dir, 42, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reaped {
		t.Fatal("a task with no recorded moment at all was reaped")
	}
	for _, c := range f.calls {
		if c == "worktree" || c == "branch" {
			t.Errorf("destructive git %q ran with no timestamp to age off (calls=%v)", c, f.calls)
		}
	}
}
