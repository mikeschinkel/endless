package monitor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// E-1940's acceptance regression, end to end on a REAL repository whose default
// branch is `master`.
//
// It is one test rather than several because the two halves only mean anything
// together: the marker must appear for genuinely unlanded work AND clear once
// the work has landed. A test that only proved the second could be passed by a
// probe that reports everything settled — which is precisely the bug.
//
// The landing is staged the way `worktree land` really does it, including the
// part that broke every SHA-identity probe: the branch is rebased, the base is
// fast-forwarded to it, and the base's history is later rewritten, so the
// recorded merge SHA ends up reachable from the BRANCH and not from the base.
// That is not a contrived shape — it is the state 80 of this repo's 599
// recorded landings were found in.

// masterFixture is a project on `master` with one task worktree, plus a
// database carrying the project and task rows the ◆ path reads.
type masterFixture struct {
	root      string
	worktree  string
	branch    string
	db        *sql.DB
	projectID int64
	taskID    int64
}

func newMasterFixture(t *testing.T) *masterFixture {
	t.Helper()
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)
	bindFaultsForTest(t)

	root := fixtureRepo(t, "master")
	const taskID = 42
	branch := "task/42"
	worktree := filepath.Join(root, ".endless", "worktrees", "e-42")
	mustGit(t, root, "worktree", "add", "-b", branch, worktree)

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'fixture', ?, 'active', '2026-01-01T00:00:00', '2026-01-01T00:00:00')`,
		root,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id)
		 VALUES (?, 1, 'probe', 'now', 'underway', 1)`, taskID,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(SetTestDB(db))

	return &masterFixture{
		root: root, worktree: worktree, branch: branch,
		db: db, projectID: 1, taskID: taskID,
	}
}

// commitInWorktree adds a file on the task branch — the user's work.
func (f *masterFixture) commitInWorktree(t *testing.T, name, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.worktree, name), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	mustGit(t, f.worktree, "add", name)
	mustGit(t, f.worktree, "commit", "-m", subject)
}

// land performs what `endless worktree land` performs — rebase the branch onto
// the base, fast-forward the base to it — and records the landing exactly as
// the events bridge does, from the BASE's HEAD after the merge.
func (f *masterFixture) land(t *testing.T, landedAt time.Time) string {
	t.Helper()
	mustGit(t, f.worktree, "rebase", "master")
	mustGit(t, f.root, "merge", "--ff-only", f.branch)
	sha := mustGit(t, f.root, "rev-parse", "HEAD")
	if _, err := f.db.Exec(
		`INSERT INTO task_landings (task_id, base_branch, merge_commit_sha, landed_at)
		 VALUES (?, 'master', ?, ?)`,
		f.taskID, sha, landedAt.UTC().Format("2006-01-02T15:04:05"),
	); err != nil {
		t.Fatalf("record landing: %v", err)
	}
	return sha
}

// TestMasterProjectMarksUnlandedWorkAndClearsAfterLanding is the acceptance
// case. Before E-1940 neither half worked on this repo: `rev-list main..HEAD`
// exits 128 where there is no `main`, and the fail-open predicate rendered that
// as the all-clear, so ◆ could never appear whatever the worktree held.
func TestMasterProjectMarksUnlandedWorkAndClearsAfterLanding(t *testing.T) {
	ctx := context.Background()
	f := newMasterFixture(t)

	// The probe the fix replaced cannot even run here. Asserting that pins WHY
	// the hardcoded base was a bug rather than a stylistic complaint.
	if _, err := runGit(ctx, f.worktree, "rev-list", "main..HEAD", "--count"); err == nil {
		t.Fatal("fixture is not actually a non-main repo: `main..HEAD` succeeded")
	}

	f.commitInWorktree(t, "feature.txt", "E-42: the work")

	// Before the job's first pass the display has nothing to read, and says so
	// rather than guessing (E-2128). This is the `~` state, on a worktree that
	// genuinely holds unlanded work — which is exactly why it must not render as
	// the blank "all landed" the pre-ED-1589 code would have drawn.
	if d := TaskWorktreeUnsettledDetail(ctx, f.projectID, f.taskID); d.UnsettledKnown() {
		t.Fatalf("a cold cache must leave the verdict unknown: %+v (%s)", d, d.Reason())
	}

	d := f.refreshAndRead(t)
	if !d.Unsettled() || !d.IsUnlanded() {
		t.Fatalf("genuinely unlanded work must mark the row: %+v (%s)", d, d.Reason())
	}
	if d.IsUndetermined() {
		t.Fatalf("the probe could not run on a master repo: %s", d.UndeterminedReason())
	}
	if !d.UnsettledKnown() {
		t.Error("the job ran, so this verdict is an answer and not a placeholder")
	}
	if d.Base != "master" {
		t.Errorf("Base = %q, want master", d.Base)
	}
	if d.UnlandedCount != 1 {
		t.Errorf("UnlandedCount = %d, want 1", d.UnlandedCount)
	}

	sha := f.land(t, time.Now().Add(-30*24*time.Hour))

	if d := f.refreshAndRead(t); d.Unsettled() {
		t.Fatalf("landed work still marked: %s", d.Reason())
	}

	// Now rewrite the base's history, which is what makes the recorded SHA
	// unreachable from it. A probe that looked for the landing ON the base
	// would regress here; anchoring on the branch survives it.
	// A different message, not --no-edit: an amend that changes nothing
	// reproduces the identical object and the SHA does not move at all.
	mustGit(t, f.root, "commit", "--amend", "-m", "E-42: the work (rewritten)")
	if err := runGitAncestorCheck(f.root, sha); err == nil {
		t.Fatal("fixture did not actually detach the recorded SHA from master")
	}
	if d := f.refreshAndRead(t); d.Unsettled() {
		t.Fatalf("a rewritten base branch resurrected the false unlanded verdict: %s", d.Reason())
	}
	// The amend is also the cache's hardest case: it REWROTE the base's history,
	// so every settled marker had to be thrown away and recomputed. A cache that
	// had merely compared tips for inequality would have kept them.
	if d := f.refreshAndRead(t); !d.UnsettledKnown() {
		t.Error("the pass after a base rewrite left the verdict unknown")
	}
}

// refreshAndRead runs the background job's pass over the fixture repo and then
// reads the verdict the way `session status` does — cache-only, computing
// nothing. Every assertion in this file goes through both halves, because the
// claim E-2128 makes is about the pair: one writer establishes the verdict, and
// the display reads what it wrote.
func (f *masterFixture) refreshAndRead(t *testing.T) UnsettledDetail {
	t.Helper()
	ctx := context.Background()
	// The refs moved, so the memoized base name and common dir are the only
	// things in play that a real monitor process would re-resolve on restart.
	resetDefaultBranchCache()
	if err := RefreshUnlandedCache(ctx, f.root); err != nil {
		t.Fatalf("RefreshUnlandedCache: %v", err)
	}
	return TaskWorktreeUnsettledDetail(ctx, f.projectID, f.taskID)
}

// TestMasterProjectReaperCanReapALandedWorktree is the same regression on the
// reaper. Condition 1 (a landing exists) and condition 4 (nothing unlanded)
// could not both hold before the fix, so a landed worktree was never removed —
// which is how a project accumulates them without bound.
func TestMasterProjectReaperCanReapALandedWorktree(t *testing.T) {
	ctx := context.Background()
	f := newMasterFixture(t)
	f.commitInWorktree(t, "feature.txt", "E-42: the work")
	f.land(t, time.Now().Add(-30*24*time.Hour))
	mustGit(t, f.root, "commit", "--amend", "-m", "E-42: the work (rewritten)")

	prevLive := hasLiveProcessInDir
	t.Cleanup(func() { hasLiveProcessInDir = prevLive })
	hasLiveProcessInDir = func(string) (bool, error) { return false, nil }

	cutoff := time.Now().UTC().Add(-14 * 24 * time.Hour)
	reaped, err := maybeReapWorktree(ctx, f.db, f.root, f.worktree, f.taskID, cutoff)
	if err != nil {
		t.Fatalf("maybeReapWorktree: %v", err)
	}
	if !reaped {
		t.Fatal("a landed, clean, abandoned worktree on a master project was not reaped")
	}
	if _, err := os.Stat(f.worktree); !os.IsNotExist(err) {
		t.Errorf("worktree directory survives the reap: %v", err)
	}
}

// runGitAncestorCheck reports nil when sha is an ancestor of the repo's HEAD.
func runGitAncestorCheck(repoDir, sha string) error {
	_, err := runGit(context.Background(), repoDir, "merge-base", "--is-ancestor", sha, "HEAD")
	return err
}
