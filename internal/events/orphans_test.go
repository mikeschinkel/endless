package events

// Tests for LedgerOrphans — the content-based, after-the-fact half of ED-1553.
//
// The scenario every case here is a variation of: a task branch forks off main
// at a ledger commit; main then amends that commit, appending another event to
// the same segment. The branch is now carrying a stale prefix of a file main has
// grown, under a SHA main no longer has. Land's orphan-drop removes that when it
// sits in an unbroken run at the branch's base; `endless worktree diagnose` has
// to be able to say so, and to say when it does NOT.

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSeg replaces the ledger segment's whole content and stages nothing.
func writeSeg(t *testing.T, root, segRel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, segRel), []byte(content), 0644); err != nil {
		t.Fatalf("write segment: %v", err)
	}
}

// forkedRepo builds the standard shape: main at a ledger commit, a task branch
// forked off it, then main amends the ledger commit with one more event.
//
// Returns the repo root, the segment path, and the SHA of the branch's orphaned
// ledger commit.
func forkedRepo(t *testing.T) (root, segRel, orphanSHA string) {
	t.Helper()
	root, segRel = initRepo(t)

	// main records a ledger event.
	mustGit(t, root, "add", segRel)
	mustGit(t, root, "commit", "-q", "-m", LedgerCommitSubject)
	orphanSHA = mustGit(t, root, "rev-parse", "HEAD")

	// A task branch forks off exactly there.
	mustGit(t, root, "checkout", "-q", "-b", "task/1957")
	mustGit(t, root, "checkout", "-q", "main")

	// main appends another event and amends the ledger tip, as canAmend does.
	writeSeg(t, root, segRel,
		`{"v":"1","kind":"task.created"}`+"\n"+`{"v":"1","kind":"task.claimed"}`+"\n")
	mustGit(t, root, "add", segRel)
	mustGit(t, root, "commit", "-q", "--amend", "-m", LedgerCommitSubject)

	mustGit(t, root, "checkout", "-q", "task/1957")
	return root, segRel, orphanSHA
}

func commitFile(t *testing.T, root, rel, content, subject string) string {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	mustGit(t, root, "add", rel)
	mustGit(t, root, "commit", "-q", "-m", subject)
	return mustGit(t, root, "rev-parse", "HEAD")
}

func TestLedgerOrphans_PrefixRunAtBaseIsContiguous(t *testing.T) {
	root, _, orphanSHA := forkedRepo(t)
	commitFile(t, root, "src.txt", "work\n", "E-1957: real work")

	rpt, err := LedgerOrphans(root, "main", "task/1957")
	if err != nil {
		t.Fatalf("LedgerOrphans: %v", err)
	}
	if len(rpt.Orphans) != 1 || rpt.Orphans[0] != orphanSHA {
		t.Fatalf("orphans = %v, want [%s]", rpt.Orphans, orphanSHA)
	}
	if !rpt.ContiguousAtBase {
		t.Fatal("a single orphan at the branch base must read as contiguous")
	}
	if rpt.MidBranch {
		t.Fatal("nothing precedes the orphan, so it cannot be mid-branch")
	}
	if rpt.LastContiguousOrphan != orphanSHA {
		t.Fatalf("LastContiguousOrphan = %q, want %q",
			rpt.LastContiguousOrphan, orphanSHA)
	}
}

func TestLedgerOrphans_OrphanBehindRealWorkIsMidBranch(t *testing.T) {
	// The shape land deliberately refuses to fix: dropping a commit that sits
	// behind someone's work can take the work with it, so the drop is limited
	// to an unbroken run at the base and this case is reported instead.
	root, segRel := initRepo(t)
	mustGit(t, root, "add", segRel)
	mustGit(t, root, "commit", "-q", "-m", LedgerCommitSubject)

	mustGit(t, root, "checkout", "-q", "-b", "task/1957")
	commitFile(t, root, "src.txt", "work\n", "E-1957: real work")
	// A ledger commit AFTER the user's work. Its segment is what main will
	// hold the first two events of.
	writeSeg(t, root, segRel,
		`{"v":"1","kind":"task.created"}`+"\n"+`{"v":"1","kind":"task.claimed"}`+"\n")
	mustGit(t, root, "add", segRel)
	mustGit(t, root, "commit", "-q", "-m", LedgerCommitSubject)
	midSHA := mustGit(t, root, "rev-parse", "HEAD")

	// main records the same two events and one more.
	mustGit(t, root, "checkout", "-q", "main")
	writeSeg(t, root, segRel,
		`{"v":"1","kind":"task.created"}`+"\n"+
			`{"v":"1","kind":"task.claimed"}`+"\n"+
			`{"v":"1","kind":"task.landed"}`+"\n")
	mustGit(t, root, "add", segRel)
	mustGit(t, root, "commit", "-q", "-m", LedgerCommitSubject)
	mustGit(t, root, "checkout", "-q", "task/1957")

	rpt, err := LedgerOrphans(root, "main", "task/1957")
	if err != nil {
		t.Fatalf("LedgerOrphans: %v", err)
	}
	if len(rpt.Orphans) != 1 || rpt.Orphans[0] != midSHA {
		t.Fatalf("orphans = %v, want [%s]", rpt.Orphans, midSHA)
	}
	if !rpt.MidBranch {
		t.Fatal("an orphan behind a non-orphan must be reported as mid-branch")
	}
	if rpt.ContiguousAtBase {
		t.Fatal("mid-branch and contiguous-at-base are mutually exclusive")
	}
	if rpt.LastContiguousOrphan != "" {
		t.Fatalf("LastContiguousOrphan = %q, want empty: there is no run at "+
			"the base to rebase --onto", rpt.LastContiguousOrphan)
	}
}

func TestLedgerOrphans_DivergedContentIsNotAnOrphan(t *testing.T) {
	// The branch's segment is NOT a prefix of main's — the two genuinely
	// diverged. Dropping this commit would lose an event, so it must not be
	// called an orphan however much its subject looks like one.
	root, segRel, _ := forkedRepo(t)
	writeSeg(t, root, segRel,
		`{"v":"1","kind":"task.created"}`+"\n"+`{"v":"1","kind":"BRANCH.ONLY"}`+"\n")
	mustGit(t, root, "add", segRel)
	mustGit(t, root, "commit", "-q", "--amend", "-m", LedgerCommitSubject)

	rpt, err := LedgerOrphans(root, "main", "task/1957")
	if err != nil {
		t.Fatalf("LedgerOrphans: %v", err)
	}
	if len(rpt.Orphans) != 0 {
		t.Fatalf("orphans = %v, want none", rpt.Orphans)
	}
	if len(rpt.Commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(rpt.Commits))
	}
	if rpt.Commits[0].SubsumedByBase {
		t.Fatal("diverged content must not read as subsumed")
	}
	if rpt.Commits[0].Reason == "" {
		t.Fatal("a refusal has to say which path failed the prefix test")
	}
}

func TestLedgerOrphans_SubjectAloneIsNotEnough(t *testing.T) {
	// A commit whose ledger content main already holds, but whose subject is
	// not an amendable one. Land's orphan-drop keys on the subject, so the
	// report must agree with it rather than widen the net on its own.
	root, segRel, _ := forkedRepo(t)
	mustGit(t, root, "commit", "-q", "--amend", "-m", "E-1957: not an auto-commit")

	rpt, err := LedgerOrphans(root, "main", "task/1957")
	if err != nil {
		t.Fatalf("LedgerOrphans: %v", err)
	}
	if len(rpt.Commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(rpt.Commits))
	}
	if !rpt.Commits[0].SubsumedByBase {
		t.Fatal("content is still a prefix of main's, so it is still subsumed")
	}
	if rpt.Commits[0].Amendable {
		t.Fatal("a non-auto-commit subject is not amendable")
	}
	if len(rpt.Orphans) != 0 {
		t.Fatalf("orphans = %v, want none", rpt.Orphans)
	}
	_ = segRel
}

func TestLedgerOrphans_CommitWithNoLedgerContentIsNotSubsumed(t *testing.T) {
	// "The base already holds this commit's ledger content" is unanswered, not
	// answered yes, for a commit that has no ledger content.
	root, _, _ := forkedRepo(t)
	commitFile(t, root, "src.txt", "work\n", "E-1957: real work")

	rpt, err := LedgerOrphans(root, "main", "task/1957")
	if err != nil {
		t.Fatalf("LedgerOrphans: %v", err)
	}
	var work *LedgerCommit
	for i := range rpt.Commits {
		if rpt.Commits[i].Subject == "E-1957: real work" {
			work = &rpt.Commits[i]
		}
	}
	if work == nil {
		t.Fatal("the work commit is missing from the report")
	}
	if work.SubsumedByBase {
		t.Fatal("a commit touching no ledger file must not read as subsumed")
	}
	if len(work.LedgerPaths) != 0 {
		t.Fatalf("LedgerPaths = %v, want none", work.LedgerPaths)
	}
}

func TestLedgerOrphans_NothingOnTheBranchIsAnEmptyReport(t *testing.T) {
	root, _ := initRepo(t)
	mustGit(t, root, "checkout", "-q", "-b", "task/1957")

	rpt, err := LedgerOrphans(root, "main", "task/1957")
	if err != nil {
		t.Fatalf("LedgerOrphans: %v", err)
	}
	if len(rpt.Commits) != 0 || len(rpt.Orphans) != 0 {
		t.Fatalf("expected an empty report, got %+v", rpt)
	}
	if rpt.ContiguousAtBase || rpt.MidBranch {
		t.Fatal("no orphans means neither flag is set")
	}
}

func TestLedgerOrphans_AcceptsSHAsSoTheAnswerDescribesTheFailure(t *testing.T) {
	// A land captures the SHAs at the moment it failed and diagnoses later; by
	// then the refs have usually moved. Resolving anything git resolves is what
	// makes the answer about the conflict rather than about today.
	root, _, orphanSHA := forkedRepo(t)
	commitFile(t, root, "src.txt", "work\n", "E-1957: real work")
	baseSHA := mustGit(t, root, "rev-parse", "main")
	branchSHA := mustGit(t, root, "rev-parse", "task/1957")

	// Move both refs on, as ordinary activity would.
	mustGit(t, root, "checkout", "-q", "main")
	commitFile(t, root, "later.txt", "later\n", "E-9999: unrelated")
	mustGit(t, root, "checkout", "-q", "task/1957")
	commitFile(t, root, "more.txt", "more\n", "E-1957: more work")

	rpt, err := LedgerOrphans(root, baseSHA, branchSHA)
	if err != nil {
		t.Fatalf("LedgerOrphans: %v", err)
	}
	if len(rpt.Orphans) != 1 || rpt.Orphans[0] != orphanSHA {
		t.Fatalf("orphans = %v, want [%s]", rpt.Orphans, orphanSHA)
	}
	if len(rpt.Commits) != 2 {
		t.Fatalf("commits = %d, want the 2 present at failure time",
			len(rpt.Commits))
	}
}
