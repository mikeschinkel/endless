package events

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mustGit runs git in dir and t.Fatals on failure. Used by setup helpers.
// Uses sanitizedGitEnv so a GIT_DIR set in the test process (intentionally,
// by some E-1309 tests) doesn't redirect the verification reads.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = sanitizedGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// initRepo creates a tmp dir, git-inits it on branch main, configures
// user/email, makes an initial commit, and returns the dir. The ledger
// segment file is created (empty) so CommitLedgerSegment has something
// to stage.
func initRepo(t *testing.T) (root string, segmentRel string) {
	t.Helper()
	root = t.TempDir()
	mustGit(t, root, "init", "-q", "-b", "main")
	mustGit(t, root, "config", "user.email", "test@example.com")
	mustGit(t, root, "config", "user.name", "Test")
	mustGit(t, root, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("hi\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGit(t, root, "add", "README")
	mustGit(t, root, "commit", "-q", "-m", "init")

	ledgerDir := filepath.Join(root, ".endless", LedgerDirName)
	if err := os.MkdirAll(ledgerDir, 0755); err != nil {
		t.Fatalf("mkdir ledger: %v", err)
	}
	segFile := filepath.Join(ledgerDir, "db-entries-a7f3-000001.jsonl")
	if err := os.WriteFile(segFile, []byte(`{"v":"1","kind":"task.created"}`+"\n"), 0644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
	segmentRel = filepath.Join(".endless", LedgerDirName, "db-entries-a7f3-000001.jsonl")
	return root, segmentRel
}

// appendLine appends one more JSONL line to the segment to simulate a
// subsequent Writer.Append.
func appendLine(t *testing.T, root, segmentRel, line string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, segmentRel), os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open segment for append: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func TestCommitLedgerSegment_FirstWriteCreatesCommit(t *testing.T) {
	root, segRel := initRepo(t)
	headBefore := mustGit(t, root, "rev-parse", "HEAD")

	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("CommitLedgerSegment: %v", err)
	}

	headAfter := mustGit(t, root, "rev-parse", "HEAD")
	if headBefore == headAfter {
		t.Fatalf("HEAD should advance after first ledger commit")
	}
	subj := mustGit(t, root, "log", "-1", "--format=%s")
	if subj != LedgerCommitSubject {
		t.Fatalf("unexpected commit subject: %q", subj)
	}
	status := mustGit(t, root, "status", "--porcelain", "--", segRel)
	if status != "" {
		t.Fatalf("segment should be clean after commit, got: %q", status)
	}
}

func TestCommitLedgerSegment_SecondWriteAmends(t *testing.T) {
	root, segRel := initRepo(t)

	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	parentAfterFirst := mustGit(t, root, "rev-parse", "HEAD^")
	headAfterFirst := mustGit(t, root, "rev-parse", "HEAD")

	appendLine(t, root, segRel, `{"v":"1","kind":"task.updated"}`)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("second commit: %v", err)
	}

	parentAfterSecond := mustGit(t, root, "rev-parse", "HEAD^")
	headAfterSecond := mustGit(t, root, "rev-parse", "HEAD")

	if parentAfterFirst != parentAfterSecond {
		t.Fatalf("amend should not change HEAD's parent: %q vs %q",
			parentAfterFirst, parentAfterSecond)
	}
	if headAfterFirst == headAfterSecond {
		t.Fatalf("amend should produce a new HEAD hash (content changed)")
	}
	subj := mustGit(t, root, "log", "-1", "--format=%s")
	if subj != LedgerCommitSubject {
		t.Fatalf("amended commit subject should still match: %q", subj)
	}
}

func TestCommitLedgerSegment_NonLedgerHeadStartsNewCommit(t *testing.T) {
	root, segRel := initRepo(t)

	// First ledger commit.
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("first ledger commit: %v", err)
	}
	firstLedgerHead := mustGit(t, root, "rev-parse", "HEAD")

	// User commits a feature.
	if err := os.WriteFile(filepath.Join(root, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	mustGit(t, root, "add", "feature.txt")
	mustGit(t, root, "commit", "-q", "-m", "feature work")
	featureHead := mustGit(t, root, "rev-parse", "HEAD")

	// Next ledger write should produce a new commit on top of feature, not amend feature.
	appendLine(t, root, segRel, `{"v":"1","kind":"task.updated"}`)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("post-feature ledger commit: %v", err)
	}

	postHead := mustGit(t, root, "rev-parse", "HEAD")
	parent := mustGit(t, root, "rev-parse", "HEAD^")
	if parent != featureHead {
		t.Fatalf("new ledger commit parent should be the feature commit, got %q (expected %q)",
			parent, featureHead)
	}
	if postHead == featureHead || postHead == firstLedgerHead {
		t.Fatalf("post-feature ledger commit should be a new hash")
	}
	subj := mustGit(t, root, "log", "-1", "--format=%s")
	if subj != LedgerCommitSubject {
		t.Fatalf("post-feature commit subject mismatch: %q", subj)
	}
}

func TestCommitLedgerSegment_StagedUnrelatedPathPreventsAmend(t *testing.T) {
	root, segRel := initRepo(t)

	// First ledger commit succeeds and sets HEAD to amend-eligible state.
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	headAfterFirst := mustGit(t, root, "rev-parse", "HEAD")
	parentAfterFirst := mustGit(t, root, "rev-parse", "HEAD^")

	// User stages an unrelated file.
	if err := os.WriteFile(filepath.Join(root, "user_work.txt"), []byte("user wip\n"), 0644); err != nil {
		t.Fatalf("write user_work: %v", err)
	}
	mustGit(t, root, "add", "user_work.txt")

	// Now do another ledger write. Amend would bundle user_work.txt — must
	// instead fall back to a new commit, leaving user_work.txt staged.
	appendLine(t, root, segRel, `{"v":"1","kind":"task.updated"}`)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("second commit: %v", err)
	}

	parentAfterSecond := mustGit(t, root, "rev-parse", "HEAD^")
	if parentAfterSecond != headAfterFirst {
		t.Fatalf("second commit should be a child of first ledger commit (new commit, not amend); "+
			"parent=%q expected=%q", parentAfterSecond, headAfterFirst)
	}
	if parentAfterFirst == parentAfterSecond {
		t.Fatalf("amend would have kept the same parent; we expected new commit")
	}

	// user_work.txt must still be staged, not committed.
	status := mustGit(t, root, "status", "--porcelain", "--", "user_work.txt")
	if !strings.HasPrefix(status, "A ") {
		t.Fatalf("user_work.txt should be staged-only after our commit, got: %q", status)
	}
}

func TestCommitLedgerSegment_PushedHeadStartsNewCommit(t *testing.T) {
	root, segRel := initRepo(t)

	// First ledger commit.
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	firstHead := mustGit(t, root, "rev-parse", "HEAD")

	// Simulate having pushed: create a refs/remotes/origin/main pointing at HEAD.
	// We use a local bare repo as origin so the ref is real.
	origin := t.TempDir()
	mustGit(t, origin, "init", "-q", "--bare")
	mustGit(t, root, "remote", "add", "origin", origin)
	mustGit(t, root, "push", "-q", "origin", "main")

	// Sanity: origin/main should now contain HEAD.
	contains := mustGit(t, root, "for-each-ref", "--contains", "HEAD", "refs/remotes/origin/")
	if contains == "" {
		t.Fatalf("setup: expected HEAD to be in origin/* after push")
	}

	// Next ledger write must NOT amend (HEAD is pushed).
	appendLine(t, root, segRel, `{"v":"1","kind":"task.updated"}`)
	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("post-push commit: %v", err)
	}

	parent := mustGit(t, root, "rev-parse", "HEAD^")
	if parent != firstHead {
		t.Fatalf("post-push commit should be a child of pushed HEAD; parent=%q expected=%q",
			parent, firstHead)
	}
}

func TestCommitLedgerSegment_NonGitProjectFailsLoudly(t *testing.T) {
	root := t.TempDir()
	segRel := filepath.Join(".endless", LedgerDirName, "db-entries-a7f3-000001.jsonl")
	if err := os.MkdirAll(filepath.Join(root, ".endless", LedgerDirName), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, segRel), []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write segment: %v", err)
	}

	err := CommitLedgerSegment(root, segRel)
	if err == nil {
		t.Fatalf("expected error for non-git project")
	}
	if !strings.Contains(err.Error(), "not a git work tree") {
		t.Fatalf("error message should mention non-git, got: %v", err)
	}
}

// --- E-1309: env sanitization + worktree-detection guard -----------------

// addWorktree creates a linked worktree off `root` and returns its
// absolute path. Used by the worktree-redirect tests.
func addWorktree(t *testing.T, root string) string {
	t.Helper()
	wtPath := filepath.Join(t.TempDir(), "wt")
	mustGit(t, root, "worktree", "add", "-q", "-b", "task/wt", wtPath)
	return wtPath
}

// TestCommitLedgerSegment_GitDirEnvIgnored sets GIT_DIR in the test
// process's env pointing at a bogus path. Without the E-1309 fix, the
// subprocess inherits it and `git -C <projectRoot>` is silently
// redirected → either a commit error or a commit landing in the wrong
// repo. With the fix, the env var is stripped at the subprocess
// boundary and the commit lands in projectRoot's repo as expected.
func TestCommitLedgerSegment_GitDirEnvIgnored(t *testing.T) {
	root, segRel := initRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "nonexistent-gitdir"))

	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("CommitLedgerSegment with bogus GIT_DIR: %v", err)
	}

	subj := mustGit(t, root, "log", "-1", "--format=%s")
	if subj != LedgerCommitSubject {
		t.Fatalf("commit should have landed in projectRoot's repo; subj=%q", subj)
	}
}

// TestCommitLedgerSegment_WorktreeProjectRootRefused passes a linked
// worktree's path as projectRoot. The guard should refuse with a
// "linked worktree" error rather than committing on the worktree's
// branch. Belt to the suspenders of env sanitization: catches any
// path that resolves into a worktree, regardless of how it got there.
func TestCommitLedgerSegment_WorktreeProjectRootRefused(t *testing.T) {
	root, _ := initRepo(t)
	wt := addWorktree(t, root)

	// Seed a ledger segment in the worktree so the path exists; the guard
	// should fire before any staging happens.
	wtLedgerDir := filepath.Join(wt, ".endless", LedgerDirName)
	if err := os.MkdirAll(wtLedgerDir, 0755); err != nil {
		t.Fatalf("mkdir worktree ledger: %v", err)
	}
	wtSeg := filepath.Join(wtLedgerDir, "db-entries-a7f3-000001.jsonl")
	if err := os.WriteFile(wtSeg, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write worktree segment: %v", err)
	}
	segRel := filepath.Join(".endless", LedgerDirName, "db-entries-a7f3-000001.jsonl")

	wtHeadBefore := mustGit(t, wt, "rev-parse", "HEAD")

	err := CommitLedgerSegment(wt, segRel)
	if err == nil {
		t.Fatalf("expected refusal when projectRoot is a linked worktree")
	}
	if !strings.Contains(err.Error(), "linked worktree") {
		t.Fatalf("error should mention 'linked worktree', got: %v", err)
	}

	wtHeadAfter := mustGit(t, wt, "rev-parse", "HEAD")
	if wtHeadBefore != wtHeadAfter {
		t.Fatalf("worktree HEAD must not advance when guard refuses; before=%q after=%q",
			wtHeadBefore, wtHeadAfter)
	}
}

// TestCommitLedgerSegment_GitDirPointingAtWorktreeStillLandsOnMain is
// the full E-1309 regression test: the parent process has GIT_DIR set
// to a linked worktree's gitdir (simulating the production bug). The
// auto-commit's projectRoot is main. Without the fix, the commit lands
// on the worktree's branch. With the fix, the env is stripped and the
// commit lands on main's HEAD as expected; the worktree's HEAD does
// not advance.
func TestCommitLedgerSegment_GitDirPointingAtWorktreeStillLandsOnMain(t *testing.T) {
	root, segRel := initRepo(t)
	wt := addWorktree(t, root)

	wtGitDir := mustGit(t, wt, "rev-parse", "--git-dir")
	if !filepath.IsAbs(wtGitDir) {
		wtGitDir = filepath.Join(wt, wtGitDir)
	}
	t.Setenv("GIT_DIR", wtGitDir)

	mainHeadBefore := mustGit(t, root, "rev-parse", "HEAD")
	wtHeadBefore := mustGit(t, wt, "rev-parse", "HEAD")

	if err := CommitLedgerSegment(root, segRel); err != nil {
		t.Fatalf("CommitLedgerSegment with GIT_DIR pointing at worktree: %v", err)
	}

	mainHeadAfter := mustGit(t, root, "rev-parse", "HEAD")
	wtHeadAfter := mustGit(t, wt, "rev-parse", "HEAD")

	if mainHeadAfter == mainHeadBefore {
		t.Fatalf("main HEAD should advance; before=%q after=%q",
			mainHeadBefore, mainHeadAfter)
	}
	if wtHeadAfter != wtHeadBefore {
		t.Fatalf("worktree HEAD must NOT advance (this is the bug); before=%q after=%q",
			wtHeadBefore, wtHeadAfter)
	}
	mainSubj := mustGit(t, root, "log", "-1", "--format=%s")
	if mainSubj != LedgerCommitSubject {
		t.Fatalf("main's HEAD subject should be ledger; got %q", mainSubj)
	}
}

// TestSanitizedGitEnv_StripsRedirectVars is a unit test for the env
// builder. Sets each git-redirect var to a sentinel value in the test
// process, asserts none of them appear in sanitizedGitEnv's output,
// and asserts an unrelated var (HOME) is still passed through.
func TestSanitizedGitEnv_StripsRedirectVars(t *testing.T) {
	for _, k := range gitRedirectVars {
		t.Setenv(k, "sentinel-"+k)
	}
	t.Setenv("HOME_E1309_PROBE", "kept")

	got := sanitizedGitEnv()
	gotMap := make(map[string]string, len(got))
	for _, kv := range got {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		gotMap[kv[:eq]] = kv[eq+1:]
	}

	for _, k := range gitRedirectVars {
		if _, present := gotMap[k]; present {
			t.Errorf("sanitizedGitEnv leaked %s=%q", k, gotMap[k])
		}
	}
	if gotMap["HOME_E1309_PROBE"] != "kept" {
		t.Errorf("sanitizedGitEnv dropped unrelated var; got %q", gotMap["HOME_E1309_PROBE"])
	}
}
