package events

import (
	"strings"
	"testing"
)

// E-2243: every passing verify run's report is committed on main as a commit of
// its own. A history of runs is the point, so these pin that a run never folds
// into an earlier one, never lands on a task branch, and never sweeps up
// whatever else happens to be staged.

const verifyReportDir = ".endless/tasks/e-42/"

func TestCommitVerifyReport_EachRunIsANewCommit(t *testing.T) {
	root, _ := initRepo(t)
	first := verifyReportDir + "verify-20261006T120000.000000Z-abc1234.ctrf.json"
	second := verifyReportDir + "verify-20261006T120500.000000Z-def5678.ctrf.json"

	writeAt(t, root, first, "{}\n")
	if err := CommitVerifyReport(root, first, "E-42"); err != nil {
		t.Fatalf("first CommitVerifyReport: %v", err)
	}
	firstSHA := mustGit(t, root, "rev-parse", "HEAD")

	writeAt(t, root, second, "{}\n")
	if err := CommitVerifyReport(root, second, "E-42"); err != nil {
		t.Fatalf("second CommitVerifyReport: %v", err)
	}

	// An amend would replace the first commit; a new commit has it as parent.
	if parent := mustGit(t, root, "rev-parse", "HEAD~1"); parent != firstSHA {
		t.Fatalf("second run amended the first: HEAD~1=%s, first run=%s", parent, firstSHA)
	}
	if got, want := mustGit(t, root, "log", "-1", "--format=%s"),
		"Endless: verify E-42 verify-20261006T120500.000000Z-def5678.ctrf.json"; got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
	if names := mustGit(t, root, "show", "--name-only", "--format=", "HEAD"); names != second {
		t.Errorf("second commit carries %q, want only %q", names, second)
	}
	if names := mustGit(t, root, "show", "--name-only", "--format=", "HEAD~1"); names != first {
		t.Errorf("first commit carries %q, want only %q", names, first)
	}
}

func TestCommitVerifyReport_RefusesALinkedWorktree(t *testing.T) {
	root, _ := initRepo(t)
	wt := addWorktree(t, root)
	rel := verifyReportDir + "verify-20261006T120000.000000Z-abc1234.ctrf.json"
	writeAt(t, wt, rel, "{}\n")
	before := mustGit(t, wt, "rev-parse", "HEAD")

	err := CommitVerifyReport(wt, rel, "E-42")
	if err == nil {
		t.Fatal("committed on a linked worktree; want a refusal")
	}
	if !strings.Contains(err.Error(), "linked worktree") {
		t.Errorf("error should name the linked worktree, got: %v", err)
	}
	if after := mustGit(t, wt, "rev-parse", "HEAD"); after != before {
		t.Error("the worktree's branch advanced")
	}
}

func TestCommitVerifyReport_LeavesUnrelatedStagedWorkAlone(t *testing.T) {
	root, _ := initRepo(t)
	writeAt(t, root, "unrelated.txt", "mine\n")
	mustGit(t, root, "add", "unrelated.txt")

	rel := verifyReportDir + "verify-20261006T120000.000000Z-abc1234.ctrf.json"
	writeAt(t, root, rel, "{}\n")
	if err := CommitVerifyReport(root, rel, "E-42"); err != nil {
		t.Fatalf("CommitVerifyReport: %v", err)
	}

	if names := mustGit(t, root, "show", "--name-only", "--format=", "HEAD"); names != rel {
		t.Errorf("commit carries %q, want only %q", names, rel)
	}
	if staged := mustGit(t, root, "diff", "--cached", "--name-only"); staged != "unrelated.txt" {
		t.Errorf("staged after commit = %q, want unrelated.txt still staged", staged)
	}
}
