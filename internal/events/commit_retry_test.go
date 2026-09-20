package events

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// E-2137 added the index.lock retry to commitPaths.
//
// The path had none, and got away with it because the only writer was the
// ledger auto-commit. That task routed every document mirror through here too:
// a `task update` now stages a file on main while another session's `task add`
// stages its ledger segment there, and two writers on one index is the textbook
// case. `land` has carried LAND_MAX_RETRIES for exactly this since E-987, so the
// discipline already existed in the codebase — it simply was not on this path.
//
// (It is not hypothetical either: writing these very tests, a `git mv` in this
// repository failed with "Unable to create index.lock: File exists".)

// holdIndexLock creates .git/index.lock and returns a function that releases it.
func holdIndexLock(t *testing.T, root string) func() {
	t.Helper()
	lock := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lock, []byte{}, 0644); err != nil {
		t.Fatalf("create index.lock: %v", err)
	}
	return func() { _ = os.Remove(lock) }
}

// noSleep replaces the backoff for the duration of a test, so a test that
// exercises eight attempts does not spend two and a half seconds doing it.
func noSleep(t *testing.T) {
	t.Helper()
	prev := sleepFn
	sleepFn = func(time.Duration) {}
	t.Cleanup(func() { sleepFn = prev })
}

func TestIsIndexLocked(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"the lock file by name",
			errors.New("git commit: fatal: Unable to create '/p/.git/index.lock': File exists"), true},
		{"git's advisory sentence",
			errors.New("Another git process seems to be running in this repository"), true},
		{"an unrelated fatal",
			errors.New("git commit: fatal: bad revision 'HEAD'"), false},
		{"nothing to commit",
			errors.New("nothing to commit, working tree clean"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isIndexLocked(tc.err); got != tc.want {
				t.Errorf("isIndexLocked(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestCommitPathsSucceedsOnceTheLockClears is the behaviour the retry exists
// for: a contended write waits out the other process rather than failing the
// user's command.
func TestCommitPathsSucceedsOnceTheLockClears(t *testing.T) {
	root, segmentRel := initRepo(t)
	release := holdIndexLock(t, root)

	// Release the lock from inside the backoff, on the second attempt — the
	// shape of a real contention, where somebody else's commit finishes while
	// we are waiting.
	attempts := 0
	prev := sleepFn
	sleepFn = func(time.Duration) {
		attempts++
		if attempts == 2 {
			release()
		}
	}
	t.Cleanup(func() { sleepFn = prev })

	if err := CommitLedgerSegment(root, segmentRel); err != nil {
		t.Fatalf("CommitLedgerSegment under contention: %v", err)
	}
	if attempts < 2 {
		t.Errorf("committed after %d backoffs; the lock was held for two", attempts)
	}
	if got := mustGit(t, root, "log", "-1", "--format=%s"); got != LedgerCommitSubject {
		t.Errorf("HEAD subject = %q, want %q", got, LedgerCommitSubject)
	}
}

// TestCommitPathsGivesUpOnAStuckLock pins the other half: a lock nobody is ever
// going to release must surface as an error in about two seconds, not hang the
// command. A retry with no cap would turn a crashed git into a wedged CLI.
func TestCommitPathsGivesUpOnAStuckLock(t *testing.T) {
	root, segmentRel := initRepo(t)
	defer holdIndexLock(t, root)()
	noSleep(t)

	err := CommitLedgerSegment(root, segmentRel)
	if err == nil {
		t.Fatal("committed with the index locked; want an error")
	}
	if !isIndexLocked(err) {
		t.Errorf("error does not read as lock contention: %v", err)
	}
}

// TestCommitPathsDoesNotRetryAnUnrelatedFailure pins that the retry is narrow.
// A misrouted commit (E-1309) must fail on its first attempt with its own
// message, not be mistaken for contention and tried eight times.
func TestCommitPathsDoesNotRetryAnUnrelatedFailure(t *testing.T) {
	root, _ := initRepo(t)
	noSleep(t)

	slept := 0
	prev := sleepFn
	sleepFn = func(time.Duration) { slept++ }
	t.Cleanup(func() { sleepFn = prev })

	err := CommitDoc(root, ".endless/tasks/e-1/plan.md", "Endless: add plan for E-1")
	if err == nil {
		t.Fatal("committed a file that does not exist; want an error")
	}
	if slept != 0 {
		t.Errorf("backed off %d times on a non-contention failure; want 0", slept)
	}
}

// TestCommitDocPathsMakesOneCommit covers the entry point the sweep uses: many
// mirror files, spanning many directories, as a single commit.
func TestCommitDocPathsMakesOneCommit(t *testing.T) {
	root, _ := initRepo(t)

	rels := []string{
		".endless/tasks/e-1/plan.md",
		".endless/tasks/e-2/outcome.md",
		".endless/decisions/ED-3.md",
	}
	for _, rel := range rels {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte("body\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	before := mustGit(t, root, "rev-list", "--count", "HEAD")
	if err := CommitDocPaths(root, rels, "Endless: consolidate document mirrors"); err != nil {
		t.Fatalf("CommitDocPaths: %v", err)
	}
	after := mustGit(t, root, "rev-list", "--count", "HEAD")
	if before == after {
		t.Fatal("nothing was committed")
	}
	if got := mustGit(t, root, "log", "-1", "--format=%s"); got != "Endless: consolidate document mirrors" {
		t.Errorf("subject = %q", got)
	}
	names := mustGit(t, root, "show", "--name-only", "--format=", "HEAD")
	for _, rel := range rels {
		if !strings.Contains(names, rel) {
			t.Errorf("commit does not carry %s; it carries:\n%s", rel, names)
		}
	}
}

// TestCommitDocPathsIsANoOpForNothing pins that an empty sweep commits nothing
// rather than creating an empty commit on main every pass.
func TestCommitDocPathsIsANoOpForNothing(t *testing.T) {
	root, _ := initRepo(t)
	before := mustGit(t, root, "rev-parse", "HEAD")
	if err := CommitDocPaths(root, nil, "Endless: nothing"); err != nil {
		t.Fatalf("CommitDocPaths(nil): %v", err)
	}
	if after := mustGit(t, root, "rev-parse", "HEAD"); after != before {
		t.Errorf("HEAD moved for an empty path list")
	}
}

// E-2137, WARN-0001 incident 1534: the doc-mirror sweep failed on every pass in
// a project whose mirrors were never committed.
//
// Relocating a mirror stages both names so git records a move. When the OLD name
// was untracked, moving it leaves a path that is neither in the working tree nor
// in the index — a no-op by definition, which `git add` nonetheless treats as a
// fatal unmatched pathspec, failing the commit and the whole sweep.
//
// Invisible in Endless's own repository, where every mirror is committed. The
// default anywhere else.

func TestCommitDocPathsSkipsAPathThatWasNeverTracked(t *testing.T) {
	root, _ := initRepo(t)

	// The shape the sweep produces: an untracked legacy mirror, relocated.
	legacy := ".endless/plans/E-1173.md"
	consolidated := ".endless/tasks/e-1173/plan.md"
	writeAt(t, root, legacy, "body\n")
	moveAt(t, root, legacy, consolidated)

	if err := CommitDocPaths(root, []string{legacy, consolidated},
		"Endless: consolidate document mirrors under .endless/tasks/"); err != nil {
		t.Fatalf("CommitDocPaths over a never-tracked legacy path: %v", err)
	}

	names := mustGit(t, root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(names, consolidated) {
		t.Errorf("the relocated mirror was not committed; commit carries:\n%s", names)
	}
	// Scoped to the mirror paths: initRepo leaves an untracked ledger dir, which
	// is the fixture's, not this commit's business.
	if got := mustGit(t, root, "status", "--porcelain", "--", ".endless/plans", ".endless/tasks"); got != "" {
		t.Errorf("mirror paths dirty after the sweep:\n%s", got)
	}
}

// The other half: when the old name WAS tracked, its deletion must still be
// staged, or the commit records an add and leaves the original behind — which
// would be a duplicate, not a move.
func TestCommitDocPathsStagesTheDeletionOfATrackedOriginal(t *testing.T) {
	root, _ := initRepo(t)

	legacy := ".endless/plans/E-1173.md"
	consolidated := ".endless/tasks/e-1173/plan.md"
	writeAt(t, root, legacy, "body\n")
	mustGit(t, root, "add", legacy)
	mustGit(t, root, "commit", "-q", "-m", "the legacy mirror, committed")

	moveAt(t, root, legacy, consolidated)

	if err := CommitDocPaths(root, []string{legacy, consolidated},
		"Endless: consolidate document mirrors under .endless/tasks/"); err != nil {
		t.Fatalf("CommitDocPaths: %v", err)
	}

	if err := runGit(root, "cat-file", "-e", "HEAD:"+legacy); err == nil {
		t.Error("the legacy path survives at HEAD; the move was recorded as a copy")
	}
	if got := mustGit(t, root, "status", "--porcelain", "--", ".endless/plans", ".endless/tasks"); got != "" {
		t.Errorf("mirror paths dirty:\n%s", got)
	}
}

// An all-no-op list must not create an empty commit on main every pass.
func TestCommitDocPathsCommitsNothingWhenNoPathIsStageable(t *testing.T) {
	root, _ := initRepo(t)
	before := mustGit(t, root, "rev-parse", "HEAD")

	if err := CommitDocPaths(root, []string{".endless/plans/E-9999.md"},
		"Endless: consolidate document mirrors under .endless/tasks/"); err != nil {
		t.Fatalf("CommitDocPaths over a path that does not exist: %v", err)
	}

	if after := mustGit(t, root, "rev-parse", "HEAD"); after != before {
		t.Error("HEAD moved for a list git could not stage anything from")
	}
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func moveAt(t *testing.T, root, from, to string) {
	t.Helper()
	dst := filepath.Join(root, to)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Rename(filepath.Join(root, from), dst); err != nil {
		t.Fatalf("rename %s -> %s: %v", from, to, err)
	}
}
