package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E-2087's acceptance tests, on REAL git repositories.
//
// A stub cannot prove any of this. The whole claim is about what git does to
// commits when `worktree land` rebases them — the SHAs it rewrites, the diffs
// conflict resolution changes, and what `git range-diff` is then able to match.
// A fixture-driven test would only assert that the test author and the
// implementation share the same belief about that, which is exactly the belief
// that was wrong before this fix.

// unlandedFixture is a repo on `main` with a task branch forked from it.
type unlandedFixture struct {
	root   string
	branch string
}

func newUnlandedFixture(t *testing.T) *unlandedFixture {
	t.Helper()
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	root := fixtureRepo(t, "main")
	const branch = "task/42-probe"
	mustGit(t, root, "checkout", "-b", branch)
	mustGit(t, root, "checkout", "main")
	return &unlandedFixture{root: root, branch: branch}
}

// commitOn writes body to name on branch and commits it with subject, leaving
// the repo checked back out on `main`.
func (f *unlandedFixture) commitOn(t *testing.T, branch, name, body, subject string) string {
	t.Helper()
	mustGit(t, f.root, "checkout", branch)
	if err := os.WriteFile(filepath.Join(f.root, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	mustGit(t, f.root, "add", name)
	mustGit(t, f.root, "commit", "-m", subject)
	sha := mustGit(t, f.root, "rev-parse", "HEAD")
	mustGit(t, f.root, "checkout", "main")
	return sha
}

// land replays the task branch onto main under NEW SHAs and fast-forwards main
// to the replay, leaving the task branch pointing at its originals.
//
// That last part is the whole bug: `worktree land` rebases before it
// fast-forwards, and this repo is full of branches whose ref never moved with
// the rebase — so main holds a content-identical copy of every commit under a
// different hash, and the branch still holds the original.
func (f *unlandedFixture) land(t *testing.T) {
	t.Helper()
	mustGit(t, f.root, "checkout", "-b", "landing-copy", f.branch)
	mustGit(t, f.root, "rebase", "main")
	mustGit(t, f.root, "checkout", "main")
	mustGit(t, f.root, "merge", "--ff-only", "landing-copy")
	mustGit(t, f.root, "branch", "-D", "landing-copy")
}

// unlandedOnBranch runs the probe against the task branch. The probe reads
// HEAD, so the branch is checked out for the call and main restored after.
func (f *unlandedFixture) unlandedOnBranch(t *testing.T) []string {
	t.Helper()
	mustGit(t, f.root, "checkout", f.branch)
	defer mustGit(t, f.root, "checkout", "main")
	got, err := unlandedCommits(f.root, "main")
	if err != nil {
		t.Fatalf("unlandedCommits: %v", err)
	}
	return got
}

// TestRebasedLandingReadsAsLanded is the reproduction, as a test. It asserts
// BOTH halves: that the SHA-reachability probe this replaced still reports the
// landed commits (so the test would fail if the bug were merely renamed), and
// that the content comparison reports none.
func TestRebasedLandingReadsAsLanded(t *testing.T) {
	f := newUnlandedFixture(t)
	f.commitOn(t, f.branch, "feature.txt", "work\n", "E-42: the work")
	f.commitOn(t, f.branch, "more.txt", "more\n", "E-42: more work")
	// main moves on independently, so the land has to rebase rather than
	// fast-forward — which is what rewrites the SHAs.
	f.commitOn(t, "main", "other.txt", "elsewhere\n", "someone else's commit")
	f.land(t)

	mustGit(t, f.root, "checkout", f.branch)
	defer mustGit(t, f.root, "checkout", "main")

	if n := mustGit(t, f.root, "rev-list", "--count", "main..HEAD"); n != "2" {
		t.Fatalf("fixture did not reproduce the bug: `main..HEAD` = %s, want 2", n)
	}

	got, err := unlandedCommits(f.root, "main")
	if err != nil {
		t.Fatalf("unlandedCommits: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("rebase-landed commits still read as unlanded: %v", got)
	}
}

// TestConflictResolvedLandingReadsAsLanded covers E-2089's correction: patch-id
// is not enough. `worktree land` rebases onto a MOVING base, and resolving a
// conflict changes the diff, after which the branch's copy and the landed copy
// share neither SHA nor patch-id. The test asserts `git cherry` still calls it
// unlanded, so it fails if the implementation ever falls back to patch-id.
func TestConflictResolvedLandingReadsAsLanded(t *testing.T) {
	f := newUnlandedFixture(t)
	f.commitOn(t, f.branch, "shared.txt", "one\ntwo\n", "E-42: extend shared.txt")

	// The landed copy of that commit, with the diff a conflict resolution would
	// have produced: same subject, same file, different content.
	f.commitOn(t, "main", "shared.txt", "one\ntwo\nthree\n", "E-42: extend shared.txt")

	mustGit(t, f.root, "checkout", f.branch)
	defer mustGit(t, f.root, "checkout", "main")

	cherry := mustGit(t, f.root, "cherry", "main", "HEAD")
	if !strings.HasPrefix(cherry, "+") {
		t.Fatalf("fixture does not reproduce the patch-id miss: git cherry = %q", cherry)
	}

	got, err := unlandedCommits(f.root, "main")
	if err != nil {
		t.Fatalf("unlandedCommits: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a conflict-resolved landing still reads as unlanded: %v", got)
	}
}

// TestGenuinelyUnlandedCommitIsReported is the other half of the acceptance
// case, and the one a probe that answered "settled" to everything would fail.
func TestGenuinelyUnlandedCommitIsReported(t *testing.T) {
	f := newUnlandedFixture(t)
	f.commitOn(t, f.branch, "feature.txt", "work\n", "E-42: landed work")
	f.commitOn(t, "main", "other.txt", "elsewhere\n", "someone else's commit")
	f.land(t)
	f.commitOn(t, f.branch, "later.txt", "later\n", "E-42: written after the land")

	got := f.unlandedOnBranch(t)
	if len(got) != 1 {
		t.Fatalf("unlanded = %v, want exactly the post-land commit", got)
	}
	if !strings.Contains(got[0], "E-42: written after the land") {
		t.Errorf("unlanded[0] = %q, want the post-land commit's subject", got[0])
	}
}

// TestUnlandedIsNewestFirst pins the display order. range-diff lists a range
// oldest-first; every surface that renders these lines shows history the other
// way round, and `git log`'s order is what they were built against.
func TestUnlandedIsNewestFirst(t *testing.T) {
	f := newUnlandedFixture(t)
	f.commitOn(t, f.branch, "a.txt", "a\n", "E-42: first")
	f.commitOn(t, f.branch, "b.txt", "b\n", "E-42: second")
	f.commitOn(t, "main", "other.txt", "elsewhere\n", "someone else's commit")

	got := f.unlandedOnBranch(t)
	if len(got) != 2 {
		t.Fatalf("unlanded = %v, want 2", got)
	}
	if !strings.Contains(got[0], "E-42: second") || !strings.Contains(got[1], "E-42: first") {
		t.Errorf("unlanded = %v, want newest first", got)
	}
}

// TestBaseUnmovedSinceForkReportsEveryCommit covers the shape `git range-diff`
// refuses outright: when the base has not moved since the fork, the range it
// would be compared against is empty and range-diff exits with "need two commit
// ranges" rather than reading it as zero counterparts. This is the ordinary
// state of a fresh worktree, so answering it wrong would make every new task
// undetermined.
func TestBaseUnmovedSinceForkReportsEveryCommit(t *testing.T) {
	f := newUnlandedFixture(t)
	f.commitOn(t, f.branch, "a.txt", "a\n", "E-42: first")
	f.commitOn(t, f.branch, "b.txt", "b\n", "E-42: second")

	mustGit(t, f.root, "checkout", f.branch)
	defer mustGit(t, f.root, "checkout", "main")

	if _, err := runGit(f.root, "range-diff", "--no-patch",
		mustGit(t, f.root, "merge-base", "main", "HEAD")+"..HEAD",
		mustGit(t, f.root, "merge-base", "main", "HEAD")+"..main"); err == nil {
		t.Fatal("fixture is not the empty-range shape: range-diff accepted it")
	}

	got, err := unlandedCommits(f.root, "main")
	if err != nil {
		t.Fatalf("unlandedCommits: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("unlanded = %v, want both commits", got)
	}
	if !strings.Contains(got[0], "E-42: second") {
		t.Errorf("unlanded[0] = %q, want newest first", got[0])
	}
}

// TestBranchContainedInBaseIsEmpty is the fully-settled steady state: HEAD is
// an ancestor of the base, so there is nothing to compare and nothing to land.
func TestBranchContainedInBaseIsEmpty(t *testing.T) {
	f := newUnlandedFixture(t)
	f.commitOn(t, f.branch, "feature.txt", "work\n", "E-42: the work")
	mustGit(t, f.root, "merge", "--ff-only", f.branch)

	got := f.unlandedOnBranch(t)
	if len(got) != 0 {
		t.Errorf("a branch already contained in main reported %v", got)
	}
}

// TestUnresolvableBaseIsAnError proves the probe reports "I could not tell"
// rather than "nothing to land" — the fail-closed direction every caller
// depends on.
func TestUnresolvableBaseIsAnError(t *testing.T) {
	f := newUnlandedFixture(t)
	f.commitOn(t, f.branch, "feature.txt", "work\n", "E-42: the work")

	got, err := unlandedCommits(f.root, "no-such-branch")
	if err == nil {
		t.Fatalf("a base that does not exist returned %v and no error", got)
	}
	if cmd := probeCommand(err); cmd != "git merge-base" {
		t.Errorf("probeCommand = %q, want the failing command", cmd)
	}
}

// TestWorktreeUnsettledAtSeesARebasedLanding walks the same rebase-landing
// through the public entry point, so the ◆ marker and `task unsettled` are
// covered end to end rather than only the comparison underneath them.
func TestWorktreeUnsettledAtSeesARebasedLanding(t *testing.T) {
	f := newUnlandedFixture(t)
	f.commitOn(t, f.branch, "feature.txt", "work\n", "E-42: the work")
	f.commitOn(t, "main", "other.txt", "elsewhere\n", "someone else's commit")
	f.land(t)
	mustGit(t, f.root, "checkout", f.branch)
	defer mustGit(t, f.root, "checkout", "main")

	d := WorktreeUnsettledDetailAt(f.root)
	if d.IsUndetermined() {
		t.Fatalf("probe could not run: %s", d.UndeterminedReason())
	}
	if d.Unsettled() {
		t.Fatalf("a rebase-landed worktree reads as unsettled: %s", d.Reason())
	}
	if d.Branch != f.branch {
		t.Errorf("Branch = %q, want %q", d.Branch, f.branch)
	}
}

// TestParseUnlandedRowsIgnoresPairedCommits pins the marker vocabulary the
// count depends on. `=` (identical) and `!` (paired but changed) both mean the
// commit HAS a counterpart on the base — `!` is the conflict-resolved landing
// this fix exists to stop mis-reporting — and `>` is not the branch's commit at
// all. Only `<` counts.
func TestParseUnlandedRowsIgnoresPairedCommits(t *testing.T) {
	out := strings.Join([]string{
		"  1:  aaaaaaa1 =   3:  bbbbbbb1 landed unchanged",
		"  2:  aaaaaaa2 !   4:  bbbbbbb2 landed with a conflict resolved",
		"  -:  -------- >   5:  bbbbbbb3 someone else's commit",
		"  3:  aaaaaaa3 <   -:  -------- genuinely unlanded",
	}, "\n")

	got := parseUnlandedRows(out)
	if len(got) != 1 {
		t.Fatalf("parseUnlandedRows = %v, want only the left-only row", got)
	}
	if got[0] != "aaaaaaa3 genuinely unlanded" {
		t.Errorf("parseUnlandedRows[0] = %q", got[0])
	}
}
