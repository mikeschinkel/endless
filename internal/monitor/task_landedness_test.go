package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E-2095's acceptance tests, on REAL git repositories, for the same reason
// E-2087's are: every claim here is about what git does — which commits
// range-diff can match after a rebase, and which paths a commit touched — and a
// stub would only assert that the test and the implementation share a belief.

// landednessFixture is a repo on `main` with a `task/42` branch forked from it,
// shaped the way an Endless project is: source at the top level, Endless's own
// records under .endless/.
type landednessFixture struct {
	root   string
	branch string
}

func newLandednessFixture(t *testing.T) *landednessFixture {
	t.Helper()
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	root := fixtureRepo(t, "main")
	const branch = "task/42"
	mustGit(t, root, "checkout", "-b", branch)
	mustGit(t, root, "checkout", "main")
	return &landednessFixture{root: root, branch: branch}
}

// commit writes each path and commits them all under one subject on the task
// branch, leaving the repo checked back out on `main`.
func (f *landednessFixture) commit(t *testing.T, subject string, paths ...string) {
	t.Helper()
	mustGit(t, f.root, "checkout", f.branch)
	for _, p := range paths {
		full := filepath.Join(f.root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", p, err)
		}
		if err := os.WriteFile(full, []byte(subject+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		mustGit(t, f.root, "add", p)
	}
	mustGit(t, f.root, "commit", "-m", subject)
	mustGit(t, f.root, "checkout", "main")
}

// advanceMain puts a commit on main so the branch and the base have diverged.
// Without it range-diff has an empty right-hand range and never runs.
func (f *landednessFixture) advanceMain(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	mustGit(t, f.root, "add", name)
	mustGit(t, f.root, "commit", "-m", "main moves: "+name)
}

// land replays the task branch onto main under new SHAs, the way
// `worktree land` does, leaving the branch pointing at its originals.
func (f *landednessFixture) land(t *testing.T) {
	t.Helper()
	mustGit(t, f.root, "checkout", "-b", "landing-copy", f.branch)
	mustGit(t, f.root, "rebase", "main")
	mustGit(t, f.root, "checkout", "main")
	mustGit(t, f.root, "merge", "--ff-only", "landing-copy")
	mustGit(t, f.root, "branch", "-D", "landing-copy")
}

func (f *landednessFixture) probe(t *testing.T) Landedness {
	t.Helper()
	got := TaskLandedness(f.root, []string{f.branch})
	if len(got) != 1 {
		t.Fatalf("TaskLandedness returned %d rows, want 1", len(got))
	}
	return got[0]
}

// A branch holding source main lacks is the case the whole feature exists for —
// E-1115 sat `assumed` in exactly this state and the same bug was fixed twice.
func TestLandednessReportsUnlandedSource(t *testing.T) {
	f := newLandednessFixture(t)
	f.commit(t, "E-42: the fix", "fix.go")
	f.advanceMain(t, "other.go")

	got := f.probe(t)
	if !got.BranchExists {
		t.Fatal("BranchExists = false, want true")
	}
	if got.UnlandedCount != 1 {
		t.Fatalf("UnlandedCount = %d (%v), want 1", got.UnlandedCount, got.UnlandedLog)
	}
	if got.Undetermined() {
		t.Errorf("Undetermined = true (%q / %q)", got.BaseErr, got.ProbeErr)
	}
}

// The rebase E-2087 fixed, seen from this probe: main holds a content-identical
// copy under a different SHA, and the branch is nonetheless landed.
func TestLandednessSeesRebasedLandingAsLanded(t *testing.T) {
	f := newLandednessFixture(t)
	f.commit(t, "E-42: the fix", "fix.go")
	f.advanceMain(t, "other.go")
	f.land(t)

	if got := f.probe(t); got.UnlandedCount != 0 {
		t.Errorf("UnlandedCount = %d (%v), want 0 — the rebased copy is on main",
			got.UnlandedCount, got.UnlandedLog)
	}
}

// A branch holding nothing but Endless's own plan and analysis mirrors has no
// CODE outstanding. Reporting it would put one row on the board per task branch
// in the project and bury the handful that matter.
func TestLandednessIgnoresBookkeepingOnlyBranch(t *testing.T) {
	f := newLandednessFixture(t)
	f.commit(t, "Endless: add plan for E-42", ".endless/plans/E-42.md")
	f.commit(t, "Endless: add analysis for E-42", ".endless/analyses/E-42.md")
	f.advanceMain(t, "other.go")

	got := f.probe(t)
	if got.UnlandedCount != 0 {
		t.Errorf("UnlandedCount = %d (%v), want 0 — only .endless/ commits",
			got.UnlandedCount, got.UnlandedLog)
	}
	if got.Undetermined() {
		t.Errorf("Undetermined = true (%q / %q)", got.BaseErr, got.ProbeErr)
	}
}

// The mixed branch is why the cheap pre-filter cannot be the whole answer: it
// says "this branch holds source", and only the per-commit filter can say WHICH
// of the commits that came back is the source one.
func TestLandednessReportsOnlyTheSourceCommitOnAMixedBranch(t *testing.T) {
	f := newLandednessFixture(t)
	f.commit(t, "Endless: add plan for E-42", ".endless/plans/E-42.md")
	f.commit(t, "E-42: the fix", "fix.go")
	f.advanceMain(t, "other.go")

	got := f.probe(t)
	if got.UnlandedCount != 1 {
		t.Fatalf("UnlandedCount = %d (%v), want 1", got.UnlandedCount, got.UnlandedLog)
	}
	if len(got.UnlandedLog) != 1 || !strings.Contains(got.UnlandedLog[0], "E-42: the fix") {
		t.Errorf("UnlandedLog = %v, want only the source commit", got.UnlandedLog)
	}
}

// A commit that touches both kinds of path is source. The filter may only
// remove commits it positively identified as bookkeeping.
func TestLandednessCountsAMixedCommitAsSource(t *testing.T) {
	f := newLandednessFixture(t)
	f.commit(t, "E-42: fix and record", "fix.go", ".endless/plans/E-42.md")
	f.advanceMain(t, "other.go")

	if got := f.probe(t); got.UnlandedCount != 1 {
		t.Errorf("UnlandedCount = %d (%v), want 1 — the commit touches source",
			got.UnlandedCount, got.UnlandedLog)
	}
}

// A missing branch is the reaped or never-created task. It is UNMEASURED, not
// clean, and the row must say so through BranchExists rather than through a
// zero count that reads identically to a landed branch.
func TestLandednessMissingBranchIsNotLanded(t *testing.T) {
	f := newLandednessFixture(t)

	got := TaskLandedness(f.root, []string{"task/999"})
	if len(got) != 1 {
		t.Fatalf("TaskLandedness returned %d rows, want 1", len(got))
	}
	if got[0].BranchExists {
		t.Error("BranchExists = true for a branch that is not there")
	}
	if got[0].Undetermined() {
		t.Errorf("Undetermined = true (%q / %q)", got[0].BaseErr, got[0].ProbeErr)
	}
}

// Rows come back in the order asked, because the caller zips them against its
// own task list.
func TestLandednessPreservesRequestOrder(t *testing.T) {
	f := newLandednessFixture(t)
	f.commit(t, "E-42: the fix", "fix.go")

	want := []string{"task/999", f.branch, "task/998"}
	got := TaskLandedness(f.root, want)
	if len(got) != len(want) {
		t.Fatalf("TaskLandedness returned %d rows, want %d", len(got), len(want))
	}
	for i, b := range want {
		if got[i].Branch != b {
			t.Errorf("row %d branch = %q, want %q", i, got[i].Branch, b)
		}
	}
}

// A repository whose default branch cannot be resolved makes every row UNKNOWN.
// Substituting "main" here is the bug DefaultBranch exists to remove, and it
// fails in the one direction that matters: a false all-clear.
func TestLandednessUnresolvedBaseIsUndetermined(t *testing.T) {
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	root := t.TempDir()
	got := TaskLandedness(root, []string{"task/42"})
	if len(got) != 1 {
		t.Fatalf("TaskLandedness returned %d rows, want 1", len(got))
	}
	if !got[0].Undetermined() {
		t.Fatal("Undetermined = false outside a repository, want true")
	}
	if got[0].BaseErr == "" {
		t.Error("BaseErr is empty; the row cannot explain itself")
	}
}

// A git child killed because its parent was interrupted is classified, not
// reported as a failure. The verdict stays undetermined — the probe established
// nothing — but the surfaces above it must not print "failed" about a healthy
// repository (E-2113).
func TestLandednessCarriesTheInterruptClassification(t *testing.T) {
	f := newLandednessFixture(t)
	f.commit(t, "E-42: the fix", "fix.go")

	restore := runGit
	runGit = func(dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "for-each-ref" {
			return "", fmt.Errorf("%w: signal: interrupt", ErrGitInterrupted)
		}
		return restore(dir, args...)
	}
	t.Cleanup(func() { runGit = restore })

	got := TaskLandedness(f.root, []string{f.branch})[0]
	if !got.Undetermined() {
		t.Fatal("Undetermined = false, want true — the probe established nothing")
	}
	if !got.Interrupted {
		t.Error("Interrupted = false; the surfaces above will print \"failed\"")
	}
}

func TestIsBookkeepingPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".endless/plans/E-42.md", true},
		{".endless/db-ledger/2026-09.jsonl", true},
		{".endless", true},
		{".endlessly/thing.go", false},
		{"src/endless/task_cmd.py", false},
		{"docs/.endless/notes.md", false},
	}
	for _, c := range cases {
		if got := isBookkeepingPath(c.path); got != c.want {
			t.Errorf("isBookkeepingPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
