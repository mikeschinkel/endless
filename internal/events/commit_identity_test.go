package events

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- E-1955: content-identity precondition on canAmend --------------------
//
// Precondition 2 (`for-each-ref --contains HEAD`) is a SHA-level test, so any
// rewrite of main's history blinds it: after `git pull --rebase` main's ledger
// tip is a brand-new SHA that no task branch contains, yet every task branch
// still holds the identical `.endless/db-ledger` tree and will conflict on it
// at land. These tests cover the content-level test that answers the same
// question in terms a rewrite cannot invalidate.

// initRepoWithOrigin builds the initRepo fixture plus a bare origin the repo
// tracks, so a history rewrite (upstream advance + `pull --rebase`) can be
// staged the way it happens in production.
func initRepoWithOrigin(t *testing.T) (root, segmentRel, origin string) {
	t.Helper()
	root, segmentRel = initRepo(t)
	origin = filepath.Join(t.TempDir(), "origin.git")
	mustGit(t, filepath.Dir(origin), "init", "-q", "--bare", origin)
	mustGit(t, root, "remote", "add", "origin", origin)
	mustGit(t, root, "push", "-q", "-u", "origin", "main")
	return root, segmentRel, origin
}

// upstreamAdvance pushes one unrelated commit to origin/main from a throwaway
// clone, so a subsequent `pull --rebase` in root rewrites root's local history.
func upstreamAdvance(t *testing.T, origin string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	mustGit(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	mustGit(t, clone, "config", "user.email", "other@example.com")
	mustGit(t, clone, "config", "user.name", "Other")
	mustGit(t, clone, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(clone, "upstream.txt"), []byte("upstream\n"), 0644); err != nil {
		t.Fatalf("write upstream file: %v", err)
	}
	mustGit(t, clone, "add", "-A")
	mustGit(t, clone, "commit", "-q", "-m", "upstream: someone else's work")
	mustGit(t, clone, "push", "-q", "origin", "main")
}

// TestCommitLedgerSegment_RewrittenHistoryStillAppends is the E-1955
// reproduction against the real commit path. A task branch forks at main's
// ledger tip and carries real work; origin advances; `pull --rebase` rewrites
// main. The next ledger event must APPEND — amending here rewrites the ledger
// commit the task branch's base still holds, so `git rebase main` in the task
// worktree conflicts on the ledger segment.
//
// Fails on baseline (precondition 2 sees no containing ref post-rewrite, so it
// amends); passes with the content-identity precondition.
func TestCommitLedgerSegment_RewrittenHistoryStillAppends(t *testing.T) {
	root, segRel, origin := initRepoWithOrigin(t)

	// Two ledger events before any task branch exists: the second amends the
	// first, which is how main's tip becomes an amend-eligible ledger commit.
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("first ledger commit: %v", err)
	}
	appendLine(t, root, segRel, `{"v":"1","kind":"task.updated"}`)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("second ledger commit: %v", err)
	}

	// A task branch forks at main's ledger tip and does real work on top.
	wt := filepath.Join(t.TempDir(), "wt")
	mustGit(t, root, "worktree", "add", "-q", "-b", "task/900", wt)
	if err := os.WriteFile(filepath.Join(wt, "task_work.txt"), []byte("task work\n"), 0644); err != nil {
		t.Fatalf("write task work: %v", err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-q", "-m", "E-900: real user work")

	// Origin advances; `pull --rebase` reassigns every local SHA on main.
	upstreamAdvance(t, origin)
	mustGit(t, root, "-c", "pull.rebase=true", "pull", "-q", "--rebase", "origin", "main")

	// Sanity: the rewrite blinded precondition 2 — no other ref contains HEAD.
	refs := mustGit(t, root, "for-each-ref", "--contains", "HEAD", "--format=%(refname)")
	if refs != "refs/heads/main" {
		t.Fatalf("setup: expected the rewrite to leave only main containing HEAD, got %q", refs)
	}

	tipBefore := mustGit(t, root, "rev-parse", "HEAD")

	appendLine(t, root, segRel, `{"v":"1","kind":"task.updated2"}`)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("post-rewrite ledger commit: %v", err)
	}

	parent := mustGit(t, root, "rev-parse", "HEAD^")
	if parent != tipBefore {
		t.Fatalf("post-rewrite ledger event must append, not amend; HEAD^=%q expected=%q",
			parent, tipBefore)
	}

	// The land this whole precondition exists to protect: rebasing the task
	// branch onto main must not conflict on the ledger segment.
	if _, err := runGitOutput(wt, "rebase", "main"); err != nil {
		conflicts, _ := runGitOutput(wt, "diff", "--name-only", "--diff-filter=U")
		_, _ = runGitOutput(wt, "rebase", "--abort")
		t.Fatalf("task branch rebase onto main must succeed; conflicted on: %q (%v)",
			conflicts, err)
	}
}

// TestCommitLedgerSegment_SelfReleasesAfterOneAppend proves the fix does not
// cost the flood suppression it sits inside. After the single suppressed amend
// re-anchors main, subsequent ledger events must resume amending — main's
// ledger commit count stays flat — and the land stays clean.
func TestCommitLedgerSegment_SelfReleasesAfterOneAppend(t *testing.T) {
	root, segRel, origin := initRepoWithOrigin(t)

	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("first ledger commit: %v", err)
	}
	mustGit(t, root, "branch", "task/900")

	upstreamAdvance(t, origin)
	mustGit(t, root, "-c", "pull.rebase=true", "pull", "-q", "--rebase", "origin", "main")

	// The suppressed amend: one append re-anchors main away from the tree the
	// task branch holds.
	appendLine(t, root, segRel, `{"v":"1","kind":"a"}`)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("re-anchoring ledger commit: %v", err)
	}
	anchored := mustGit(t, root, "rev-parse", "HEAD^")

	// Five more events must all fold into that one commit.
	for _, k := range []string{"b", "c", "d", "e", "f"} {
		appendLine(t, root, segRel, `{"v":"1","kind":"`+k+`"}`)
		if err := CommitLedgerSegment(root, segRel); err != nil {
			t.Fatalf("tail ledger commit %s: %v", k, err)
		}
	}

	if got := mustGit(t, root, "rev-parse", "HEAD^"); got != anchored {
		t.Fatalf("amending must resume after the single append; HEAD^=%q expected=%q",
			got, anchored)
	}
}

// ledgerExclude is the amend-scope glob CommitLedgerSegment passes.
var ledgerExclude = ".endless/" + LedgerDirName + "/*.jsonl"

// taskBranchAt creates refs/heads/task/<n> forked at `base` (the repo's root
// commit, i.e. BEFORE main's ledger commit) and commits `content` as its ledger
// segment — or, with content == "", commits an unrelated file and no ledger
// tree at all. Forking at base means the branch does not contain main's ledger
// tip, so precondition 2 is silent about it and whatever canAmend decides is
// the content test's doing.
func taskBranchAt(t *testing.T, root, name, base, segRel, content string) {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt-"+strings.ReplaceAll(name, "/", "-"))
	mustGit(t, root, "worktree", "add", "-q", "-b", name, wt, base)
	rel := segRel
	if content == "" {
		rel, content = "branch_only.txt", "no ledger here\n"
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, rel)), 0755); err != nil {
		t.Fatalf("mkdir branch ledger dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt, rel), []byte(content), 0644); err != nil {
		t.Fatalf("write branch ledger: %v", err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-q", "-m", "E-900: branch work")
}

// TestCanAmend_TaskBranchWithIdenticalLedgerTreeRefuses drives canAmend
// directly: main's tip is a ledger commit and a task branch — unreachable from
// main, so precondition 2 says nothing — holds a byte-identical
// `.endless/db-ledger` tree. The content test must refuse.
func TestCanAmend_TaskBranchWithIdenticalLedgerTreeRefuses(t *testing.T) {
	root, segRel := initRepo(t)
	base := mustGit(t, root, "rev-parse", "HEAD")
	segContent := readFile(t, filepath.Join(root, segRel))
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("ledger commit: %v", err)
	}
	taskBranchAt(t, root, "task/900", base, segRel, segContent)

	if got := mustGit(t, root, "for-each-ref", "--contains", "HEAD", "--format=%(refname)"); got != "refs/heads/main" {
		t.Fatalf("setup: only main should contain HEAD, got %q", got)
	}
	if mustGit(t, root, "rev-parse", "task/900:.endless/"+LedgerDirName) !=
		mustGit(t, root, "rev-parse", "HEAD:.endless/"+LedgerDirName) {
		t.Fatalf("setup: branch ledger tree should be identical to main's")
	}

	ok, err := canAmend(root, LedgerCommitSubject, ledgerExclude)
	if err != nil {
		t.Fatalf("canAmend: %v", err)
	}
	if ok {
		t.Fatalf("canAmend must refuse: task/900 holds an identical ledger tree")
	}
}

// TestCanAmend_TaskBranchWithDifferentLedgerTreeAmends is the negative half:
// a task branch whose ledger tree differs must not suppress the amend, or the
// content test would disable flood suppression outright.
func TestCanAmend_TaskBranchWithDifferentLedgerTreeAmends(t *testing.T) {
	root, segRel := initRepo(t)
	base := mustGit(t, root, "rev-parse", "HEAD")
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("ledger commit: %v", err)
	}
	taskBranchAt(t, root, "task/900", base, segRel, `{"v":"1","kind":"branch.only"}`+"\n")

	ok, err := canAmend(root, LedgerCommitSubject, ledgerExclude)
	if err != nil {
		t.Fatalf("canAmend: %v", err)
	}
	if !ok {
		t.Fatalf("canAmend must permit: no task branch holds main's ledger tree")
	}
}

// TestCanAmend_NoLedgerTreeAtHeadAmends covers the empty-OID edge: a repo whose
// HEAD carries no `.endless/db-ledger` tree at all (a CommitDoc subject, say)
// must be treated as "no match, do not suppress" rather than as an error or as
// a match against a branch that also lacks the tree.
func TestCanAmend_NoLedgerTreeAtHeadAmends(t *testing.T) {
	root, segRel := initRepo(t)
	base := mustGit(t, root, "rev-parse", "HEAD")

	// main's tip carries the ledger subject but no ledger tree.
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("changed\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGit(t, root, "commit", "-q", "-a", "-m", LedgerCommitSubject)

	// A task branch exists and it too has no ledger tree — an empty-vs-empty
	// comparison must not read as "identical".
	taskBranchAt(t, root, "task/900", base, segRel, "")

	if got, err := runGitOutput(root, "rev-parse", "--verify", "--quiet",
		"HEAD:.endless/"+LedgerDirName); err == nil {
		t.Fatalf("setup: main's tip should carry no ledger tree, got %q", strings.TrimSpace(got))
	}

	ok, err := canAmend(root, LedgerCommitSubject, ledgerExclude)
	if err != nil {
		t.Fatalf("canAmend must not error when HEAD has no ledger tree: %v", err)
	}
	if !ok {
		t.Fatalf("canAmend must permit when HEAD carries no ledger tree at all")
	}
}

// readFile reads a file or t.Fatals. Used to copy main's committed segment
// content onto a task branch verbatim.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestCanAmend_NoTaskBranchesAmends covers the missing-branch edge: a repo with
// no `refs/heads/task/*` at all must not error and must still amend.
func TestCanAmend_NoTaskBranchesAmends(t *testing.T) {
	root, segRel := initRepo(t)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("ledger commit: %v", err)
	}
	if got := mustGit(t, root, "for-each-ref", "--format=%(refname)", "refs/heads/task"); got != "" {
		t.Fatalf("setup: expected no task branches, got %q", got)
	}

	ok, err := canAmend(root, LedgerCommitSubject, ".endless/"+LedgerDirName+"/*.jsonl")
	if err != nil {
		t.Fatalf("canAmend: %v", err)
	}
	if !ok {
		t.Fatalf("canAmend must permit with no task branches present")
	}
}
