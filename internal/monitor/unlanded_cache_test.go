package monitor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
)

// E-2128 — the cache and its validity protocol, against REAL git repositories.
//
// A stub cannot prove any of this. Every rule here is a claim about what git does
// to commits — which SHAs a rebase rewrites, what `merge-base --is-ancestor`
// answers after an amend, whether a branch contained in its base has anything
// outstanding — and a fixture-driven test would only assert that the test author
// and the implementation share one belief about that. worktree_unlanded_test.go
// made the same call for the same reason.

// cacheFixture is a repo on `main` with task worktrees under
// .endless/worktrees/e-<id>, which is the layout the job enumerates.
type cacheFixture struct {
	root      string
	worktrees []string
	branches  []string
}

// newCacheFixture builds a repo with n task worktrees, each on its own branch
// forked from the base and holding one commit — so every one of them starts
// genuinely unlanded.
func newCacheFixture(t *testing.T, n int) *cacheFixture {
	t.Helper()
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)
	bindFaultsForTest(t)

	f := &cacheFixture{root: fixtureRepo(t, "main")}
	for i := 1; i <= n; i++ {
		id := 2128 + i
		branch := "task/" + strconv.Itoa(id)
		dir := filepath.Join(f.root, ".endless", "worktrees", "e-"+strconv.Itoa(id))
		mustGit(t, f.root, "worktree", "add", "-b", branch, dir)
		f.worktrees = append(f.worktrees, dir)
		f.branches = append(f.branches, branch)
		f.commitIn(t, dir, "work.txt", "E-"+strconv.Itoa(id)+": the work")
	}
	return f
}

// commitIn writes a file in dir and commits it on whatever branch dir has out.
func (f *cacheFixture) commitIn(t *testing.T, dir, name, subject string) {
	t.Helper()
	body := subject + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	mustGit(t, dir, "add", name)
	mustGit(t, dir, "commit", "-m", subject)
}

// landFromWorktree does what `endless worktree land` does: rebase the branch onto
// the base, then fast-forward the base to it. Afterwards the branch tip IS the
// base tip, which is the settled steady state.
func (f *cacheFixture) landFromWorktree(t *testing.T, i int) {
	t.Helper()
	mustGit(t, f.worktrees[i], "rebase", "main")
	mustGit(t, f.root, "merge", "--ff-only", f.branches[i])
}

// appendToBase adds a commit to `main` in the main checkout — base movement that
// only ever ADDS candidates for the branch's commits to match against.
func (f *cacheFixture) appendToBase(t *testing.T, name string) {
	t.Helper()
	f.commitIn(t, f.root, name, "someone else's commit: "+name)
}

// cache is the fixture repo's cache, resolved the way production resolves it.
func (f *cacheFixture) cache(t *testing.T) unlandedCache {
	t.Helper()
	c, err := unlandedCacheFor(context.Background(), f.root)
	if err != nil {
		t.Fatalf("unlandedCacheFor: %v", err)
	}
	return c
}

// refresh runs one pass of the background job's work over the fixture.
func (f *cacheFixture) refresh(t *testing.T) {
	t.Helper()
	// The refs moved, so drop the memos a long-lived process would have refreshed
	// only on restart. Nothing under test here is about memo lifetime.
	resetDefaultBranchCache()
	if err := RefreshUnlandedCache(context.Background(), f.root); err != nil {
		t.Fatalf("RefreshUnlandedCache: %v", err)
	}
}

// read is the display path's read: cache-only, computing nothing.
func (f *cacheFixture) read(t *testing.T, i int) unlandedLookup {
	t.Helper()
	return cachedUnlanded(context.Background(), f.worktrees[i])
}

// gitCounter records every git invocation so a test can assert which commands a
// path did NOT run. Concurrency-safe, because the job's pass computes misses
// through a worker pool.
type gitCounter struct {
	mu    sync.Mutex
	calls []string
}

func (g *gitCounter) ran(sub string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.calls {
		if c == sub {
			return true
		}
	}
	return false
}

// countGit wraps the production runGit — it still runs real git — and records the
// subcommand of every invocation.
func countGit(t *testing.T) *gitCounter {
	t.Helper()
	g := &gitCounter{}
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
		g.mu.Lock()
		g.calls = append(g.calls, args[0])
		g.mu.Unlock()
		return prev(ctx, dir, args...)
	}
	return g
}

// TestDisplayPathNeverRunsTheComparison is the property the whole task exists
// for, stated against real git: after the job has run, the cache-only read that
// backs the ◆ column must not invoke a single command of the content comparison.
//
// It should fail loudly if a future edit reintroduces a compute on the display
// path. That edit would not look like a mistake — it would look like making the
// marker more accurate — which is exactly why the assertion is written down.
func TestDisplayPathNeverRunsTheComparison(t *testing.T) {
	f := newCacheFixture(t, 2)
	f.appendToBase(t, "base.txt") // so the comparison has two non-empty ranges
	f.refresh(t)

	g := countGit(t)
	for i := range f.worktrees {
		got := f.read(t, i)
		if !got.Known {
			t.Fatalf("worktree %d: the job ran, so the verdict must be cached", i)
		}
		if len(got.Commits) != 1 {
			t.Errorf("worktree %d: cached verdict = %v, want the one unlanded commit", i, got.Commits)
		}
	}
	for _, forbidden := range []string{"range-diff", "merge-base", "log", "status"} {
		if g.ran(forbidden) {
			t.Errorf("the cache-only read ran `git %s` (%d calls total: %v)",
				forbidden, len(g.calls), g.calls)
		}
	}
}

// TestSettledVerdictSurvivesBaseAppend is the invariant the cache is built on: a
// verdict of ZERO unlanded stays true while the BRANCH tip does not move,
// however far the base advances, because base movement can only add commits for
// the branch's to match against.
//
// The assertion is not merely that the answer is still right — it is that the
// pass did NOT recompute it, since a cache that re-derives a permanent answer
// every minute is not a cache.
func TestSettledVerdictSurvivesBaseAppend(t *testing.T) {
	f := newCacheFixture(t, 1)
	f.landFromWorktree(t, 0)
	f.refresh(t)

	if got := f.read(t, 0); !got.Known || len(got.Commits) != 0 {
		t.Fatalf("a landed worktree must cache as settled: %+v", got)
	}
	marker := f.cache(t).settledPath(mustGit(t, f.worktrees[0], "rev-parse", "HEAD"))
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("settled marker missing: %v", err)
	}

	f.appendToBase(t, "later.txt")

	g := countGit(t)
	f.refresh(t)
	if g.ran("range-diff") {
		t.Errorf("the pass recomputed a settled verdict after an append-only base move: %v", g.calls)
	}
	if got := f.read(t, 0); !got.Known || len(got.Commits) != 0 {
		t.Errorf("settled verdict did not survive the append: %+v", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("settled marker was dropped by an append-only base move: %v", err)
	}
}

// TestUnsettledVerdictInvalidatesOnBaseAppend is the other half of the same
// invariant, in the direction that does NOT hold: a verdict of N>0 is valid only
// while both tips are unchanged, because base movement can shrink it.
//
// Invalidation is a path miss — the entry nests under the base tip — so the test
// asserts the shape as well as the recompute: the stale directory is pruned and a
// new one appears under the new tip.
func TestUnsettledVerdictInvalidatesOnBaseAppend(t *testing.T) {
	f := newCacheFixture(t, 1)
	f.appendToBase(t, "base.txt")
	f.refresh(t)

	oldTip := mustGit(t, f.root, "rev-parse", "main")
	head := mustGit(t, f.worktrees[0], "rev-parse", "HEAD")
	c := f.cache(t)
	if _, err := os.Stat(c.unsettledPath(oldTip, head)); err != nil {
		t.Fatalf("unsettled entry not written under the base tip: %v", err)
	}

	f.appendToBase(t, "later.txt")
	newTip := mustGit(t, f.root, "rev-parse", "main")

	g := countGit(t)
	f.refresh(t)
	if !g.ran("range-diff") {
		t.Errorf("base movement did not invalidate a non-zero verdict: %v", g.calls)
	}
	if _, err := os.Stat(c.unsettledPath(newTip, head)); err != nil {
		t.Errorf("no entry under the new base tip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.dir, unsettledSubdir, oldTip)); !os.IsNotExist(err) {
		t.Errorf("the stale base-tip directory was not pruned: %v", err)
	}
	if got := f.read(t, 0); !got.Known || len(got.Commits) != 1 {
		t.Errorf("verdict after the append = %+v, want the one unlanded commit", got)
	}
}

// TestBaseHistoryRewriteFlushesEverySettledEntry is the assumption-check the
// whole invariant rests on. "Settled is permanent" holds only while the base is
// APPEND-ONLY, and that is a claim about a repository, not a law — so an amend,
// reset or force-push of the base must throw the settled half away rather than
// keep reading it.
func TestBaseHistoryRewriteFlushesEverySettledEntry(t *testing.T) {
	f := newCacheFixture(t, 1)
	f.landFromWorktree(t, 0)
	f.refresh(t)

	head := mustGit(t, f.worktrees[0], "rev-parse", "HEAD")
	marker := f.cache(t).settledPath(head)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("settled marker missing before the rewrite: %v", err)
	}

	// An amend with a DIFFERENT message: one that changes nothing reproduces the
	// identical object and the tip does not move at all.
	mustGit(t, f.root, "commit", "--amend", "-m", "rewritten base commit")

	g := countGit(t)
	f.refresh(t)
	if !g.ran("range-diff") && !g.ran("rev-list") {
		t.Errorf("a rewritten base did not trigger a recompute: %v", g.calls)
	}
	// The branch now holds a commit the rewritten base lacks, which is the right
	// answer and the reason the old marker had to go.
	if got := f.read(t, 0); !got.Known {
		t.Errorf("verdict after the rewrite is not established: %+v", got)
	}
}

// TestBranchAmendInvalidatesOnlyItsOwnEntry pins the granularity the cache gets
// for free from keying on the branch tip: a `git commit --amend` in one worktree
// moves that worktree's key and nothing else's.
func TestBranchAmendInvalidatesOnlyItsOwnEntry(t *testing.T) {
	f := newCacheFixture(t, 2)
	f.appendToBase(t, "base.txt")
	f.refresh(t)

	untouchedHead := mustGit(t, f.worktrees[1], "rev-parse", "HEAD")
	baseTip := mustGit(t, f.root, "rev-parse", "main")
	untouchedEntry := f.cache(t).unsettledPath(baseTip, untouchedHead)
	before, err := os.Stat(untouchedEntry)
	if err != nil {
		t.Fatalf("entry for the untouched worktree missing: %v", err)
	}

	mustGit(t, f.worktrees[0], "commit", "--amend", "-m", "E-2129: amended")
	amendedHead := mustGit(t, f.worktrees[0], "rev-parse", "HEAD")

	// The amended worktree's verdict is gone; its neighbour's is not.
	if got := f.read(t, 0); got.Known {
		t.Errorf("an amended branch still read a cached verdict: %+v", got)
	}
	if got := f.read(t, 1); !got.Known {
		t.Errorf("an unrelated worktree lost its verdict to a neighbour's amend: %+v", got)
	}

	f.refresh(t)
	if _, err := os.Stat(f.cache(t).unsettledPath(baseTip, amendedHead)); err != nil {
		t.Errorf("the amended branch was not recomputed: %v", err)
	}
	after, err := os.Stat(untouchedEntry)
	if err != nil {
		t.Fatalf("the untouched entry was removed: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the untouched entry was rewritten (mtime %v → %v)", before.ModTime(), after.ModTime())
	}
}

// TestChangingTheConfiguredBaseNameInvalidatesEverything is why the base NAME
// lives in the watermark's FILENAME. Pointing a project at a different default
// branch makes every stored verdict an answer to a different question, and the
// old watermark is orphaned by the rename alone — no comparison, no version
// field, no migration.
func TestChangingTheConfiguredBaseNameInvalidatesEverything(t *testing.T) {
	f := newCacheFixture(t, 1)
	f.landFromWorktree(t, 0)
	f.refresh(t)

	head := mustGit(t, f.worktrees[0], "rev-parse", "HEAD")
	if _, err := os.Stat(f.cache(t).settledPath(head)); err != nil {
		t.Fatalf("settled marker missing: %v", err)
	}
	if got := f.read(t, 0); got.Base != "main" {
		t.Fatalf("watermark base = %q, want main", got.Base)
	}

	// A second long-lived branch, then configure it as the project's base.
	mustGit(t, f.root, "branch", "release/2.0")
	writeProjectConfig(t, f.root, "release/2.0")
	// The config is a tracked file, so the worktree sees it too; committing it
	// keeps the fixture's worktrees clean, which is not what is under test here.
	mustGit(t, f.root, "add", ".endless/config.json")
	mustGit(t, f.root, "commit", "-m", "point the project at release/2.0")

	f.refresh(t)

	got := f.read(t, 0)
	if got.Base != "release/2.0" {
		t.Errorf("watermark base = %q, want release/2.0 — the name is part of the key", got.Base)
	}
	// Exactly one watermark: the pass must remove the orphan rather than leave two
	// for readWatermark to refuse to choose between.
	entries, err := os.ReadDir(f.cache(t).dir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	var watermarks []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), watermarkPrefix) {
			watermarks = append(watermarks, e.Name())
		}
	}
	if len(watermarks) != 1 {
		t.Errorf("cache holds %d watermarks (%v), want exactly 1", len(watermarks), watermarks)
	}
	// And a branch name with a slash survives the round trip as one path
	// component, which is the whole reason the filename is escaped.
	if strings.Contains(watermarks[0], string(filepath.Separator)) {
		t.Errorf("watermark filename %q is not a single path component", watermarks[0])
	}
}

// TestMissingWatermarkIsACompleteMiss is the cold-start state, and the reason `~`
// exists: with no watermark a reader cannot address the cache at all, so every
// lookup misses — including for worktrees whose settled markers are sitting right
// there. Fail-closed, by construction rather than by a check.
func TestMissingWatermarkIsACompleteMiss(t *testing.T) {
	f := newCacheFixture(t, 2)
	f.landFromWorktree(t, 0)
	f.refresh(t)
	if got := f.read(t, 0); !got.Known {
		t.Fatalf("setup: the pass did not establish a verdict: %+v", got)
	}

	c := f.cache(t)
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), watermarkPrefix) {
			if err := os.Remove(filepath.Join(c.dir, e.Name())); err != nil {
				t.Fatalf("remove watermark: %v", err)
			}
		}
	}

	for i := range f.worktrees {
		if got := f.read(t, i); got.Known {
			t.Errorf("worktree %d read a verdict with no watermark: %+v", i, got)
		}
	}
}

// TestAmbiguousWatermarkIsACompleteMiss covers the one state readWatermark
// refuses to resolve rather than guess. Two `base-*` files can only come from a
// half-applied write, and picking one would be choosing which base branch the
// stored verdicts answer about.
func TestAmbiguousWatermarkIsACompleteMiss(t *testing.T) {
	f := newCacheFixture(t, 1)
	f.refresh(t)
	if got := f.read(t, 0); !got.Known {
		t.Fatalf("setup: no verdict cached: %+v", got)
	}

	c := f.cache(t)
	tip := mustGit(t, f.root, "rev-parse", "main")
	if err := os.WriteFile(filepath.Join(c.dir, watermarkPrefix+"master"), []byte(tip+"\n"), 0o644); err != nil {
		t.Fatalf("write second watermark: %v", err)
	}
	if got := f.read(t, 0); got.Known {
		t.Errorf("an ambiguous watermark was resolved instead of refused: %+v", got)
	}

	// And the next pass repairs it, rather than leaving the cache unreadable.
	f.refresh(t)
	if got := f.read(t, 0); !got.Known {
		t.Errorf("the pass did not repair the ambiguity: %+v", got)
	}
}

// TestCachedCountIsExactBeyondTheDisplayCap is why the stored entry is NOT
// truncated to unlandedLogLimit. The count has to stay exact — the detail view
// says how many more commits there are than it printed — and with no file format
// the only place a count can live is the line count.
func TestCachedCountIsExactBeyondTheDisplayCap(t *testing.T) {
	const n = unlandedLogLimit + 7
	f := newCacheFixture(t, 1)
	f.refresh(t)

	c := f.cache(t)
	baseTip := mustGit(t, f.root, "rev-parse", "main")
	head := mustGit(t, f.worktrees[0], "rev-parse", "HEAD")
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "aaaaaa" + strconv.Itoa(i) + " commit " + strconv.Itoa(i)
	}
	if err := c.writeEntry(baseTip, head, lines); err != nil {
		t.Fatalf("writeEntry: %v", err)
	}

	got := cachedUnlanded(context.Background(), f.worktrees[0])
	if !got.Known || len(got.Commits) != n {
		t.Fatalf("cache read returned %d commits, want the full %d", len(got.Commits), n)
	}

	d := WorktreeUnsettledAt(context.Background(), f.worktrees[0])
	if d.UnlandedCount != n {
		t.Errorf("UnlandedCount = %d, want the exact %d", d.UnlandedCount, n)
	}
	if len(d.UnlandedLog) != unlandedLogLimit {
		t.Errorf("len(UnlandedLog) = %d, want it capped at %d", len(d.UnlandedLog), unlandedLogLimit)
	}
}

// TestTruncatedUnsettledEntryReadsAsAMiss is the one mistake the cache must never
// make. An empty unsettled file is not a representation this writer produces — a
// zero verdict is a settled marker — so it can only be a truncated write, and
// reading it as zero unlanded commits would be the false all-clear in its purest
// form.
func TestTruncatedUnsettledEntryReadsAsAMiss(t *testing.T) {
	f := newCacheFixture(t, 1)
	f.refresh(t)

	c := f.cache(t)
	baseTip := mustGit(t, f.root, "rev-parse", "main")
	head := mustGit(t, f.worktrees[0], "rev-parse", "HEAD")
	if err := os.WriteFile(c.unsettledPath(baseTip, head), nil, 0o644); err != nil {
		t.Fatalf("truncate entry: %v", err)
	}

	if got := cachedUnlanded(context.Background(), f.worktrees[0]); got.Known {
		t.Errorf("a truncated entry read as an answer: %+v", got)
	}
}

// TestSettledMarkerIsEmpty pins the format that is not a format. "Fully landed"
// is a property of the branch tip and the base alone, so there is nothing to
// qualify it with — and two worktrees at one OID legitimately share the entry.
func TestSettledMarkerIsEmpty(t *testing.T) {
	f := newCacheFixture(t, 1)
	f.landFromWorktree(t, 0)
	f.refresh(t)

	head := mustGit(t, f.worktrees[0], "rev-parse", "HEAD")
	fi, err := os.Stat(f.cache(t).settledPath(head))
	if err != nil {
		t.Fatalf("settled marker missing: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("settled marker is %d bytes; presence alone is the verdict", fi.Size())
	}
}

// TestPruneDropsMarkersForVanishedWorktrees keeps the cache from growing without
// bound as worktrees are reaped. The answer for a branch tip nothing points at is
// still true, and still useless.
func TestPruneDropsMarkersForVanishedWorktrees(t *testing.T) {
	f := newCacheFixture(t, 1)
	f.refresh(t)

	c := f.cache(t)
	stale := c.settledPath("0123456789abcdef0123456789abcdef01234567")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatalf("mkdir settled: %v", err)
	}
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatalf("seed stale marker: %v", err)
	}

	f.refresh(t)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a marker matching no current worktree HEAD survived the pass: %v", err)
	}
}

// TestBaseNameEscapingRoundTrips covers the names a real project uses. A branch
// may contain `/`, and the watermark carries the name in its filename — so the
// escape has to be reversible and injective, or two base branches would share one
// watermark.
func TestBaseNameEscapingRoundTrips(t *testing.T) {
	names := []string{
		"main", "master", "trunk",
		"release/2.0", "releases/v1.2.3", "feature/a_b-c.d",
		"ünïcode", "weird name", "%", "a%2Fb",
	}
	seen := map[string]string{}
	for _, name := range names {
		esc := escapeBaseName(name)
		if strings.ContainsAny(esc, "/\\") {
			t.Errorf("escapeBaseName(%q) = %q, which is not one path component", name, esc)
		}
		if got := unescapeBaseName(esc); got != name {
			t.Errorf("round trip of %q gave %q (escaped %q)", name, got, esc)
		}
		if other, dup := seen[esc]; dup {
			t.Errorf("escapeBaseName collided: %q and %q both gave %q", name, other, esc)
		}
		seen[esc] = name
	}
}

// TestUnusableCacheDirIsAPermanentMissAndOneFault covers the read-only checkout.
// Every probe still runs and every on-demand answer is still exact; what is lost
// is the ability to remember one. That is a warning with one remedy, recorded
// once against the DIRECTORY — never once per worktree per pass.
func TestUnusableCacheDirIsAPermanentMissAndOneFault(t *testing.T) {
	f := newCacheFixture(t, 3)

	// A FILE where the cache directory needs to be: MkdirAll fails with ENOTDIR,
	// which is the same shape as a permissions refusal and does not require the
	// test to run as an unprivileged user of its own making.
	c := f.cache(t)
	if err := os.MkdirAll(filepath.Dir(c.dir), 0o755); err != nil {
		t.Fatalf("mkdir cache parent: %v", err)
	}
	if err := os.WriteFile(c.dir, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("occupy the cache path: %v", err)
	}

	resetDefaultBranchCache()
	if err := RefreshUnlandedCache(context.Background(), f.root); err != nil {
		t.Fatalf("an unwritable cache must not fail the pass: %v", err)
	}

	for i := range f.worktrees {
		if got := f.read(t, i); got.Known {
			t.Errorf("worktree %d read a verdict from an unusable cache: %+v", i, got)
		}
	}
	// The on-demand path still answers, exactly, by computing.
	d := WorktreeUnsettledDetailAt(context.Background(), f.worktrees[0])
	if d.IsUndetermined() || !d.UnlandedKnown {
		t.Errorf("the on-demand path must still answer with an unwritable cache: %+v", d)
	}

	incidents := listIncidents(t)
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents over 3 worktrees, want exactly 1: %+v", len(incidents), incidents)
	}
	if incidents[0].Code != "ERR-0012" {
		t.Errorf("code = %s, want ERR-0012", incidents[0].Code)
	}
}

// listIncidents reads every open incident from the store bindFaultsForTest wired
// up, failing the test rather than returning an error nobody would check.
func listIncidents(t *testing.T) []faults.Incident {
	t.Helper()
	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	return incidents
}

// TestPostLandWarmIsVisibleWithoutWaitingForTheJob is the moment a person
// actually watches: you land your own task, and the pane either updates or it
// does not.
//
// It works because settled/ is keyed on the branch tip ALONE and is read before
// anything else. A land moves the base past the tip the watermark records, so
// every unsettled/ entry in the repo goes unreachable at that instant — but a
// settled marker does not go through the watermark's tip at all, which is
// exactly why the settled half is not nested under it. `worktree land` writes
// that marker through the same entry point it shells to, and the next render
// reads it.
//
// Asserted with NO job pass in between, because that is the claim: the display
// must not wait an interval for the one worktree whose state definitely just
// changed.
func TestPostLandWarmIsVisibleWithoutWaitingForTheJob(t *testing.T) {
	ctx := context.Background()
	f := newCacheFixture(t, 1)
	f.appendToBase(t, "base.txt")
	f.refresh(t) // the job has run once, so the repo has a watermark

	if got := f.read(t, 0); !got.Known || len(got.Commits) != 1 {
		t.Fatalf("setup: the worktree should read as unlanded: %+v", got)
	}
	watermarkTip := mustGit(t, f.root, "rev-parse", "main")

	f.landFromWorktree(t, 0)

	// The land moved BOTH tips, so before the warm there is nothing to read —
	// which is what the row would show as `~`.
	if got := f.read(t, 0); got.Known {
		t.Fatalf("a land moves the branch tip; the old entry must not still answer: %+v", got)
	}
	if now := mustGit(t, f.root, "rev-parse", "main"); now == watermarkTip {
		t.Fatal("the fixture did not actually move the base past the watermark")
	}

	// What `worktree land` does next: ask the on-demand entry point, which
	// computes and stores. Cheap here by construction — the branch is contained
	// in its base, so the comparison short-circuits before range-diff.
	if d := WorktreeUnsettledDetailAt(ctx, f.worktrees[0]); d.Unsettled() {
		t.Fatalf("a just-landed worktree is not settled: %s", d.Reason())
	}

	// No refresh. Read exactly as the ◆ column does.
	got := f.read(t, 0)
	if !got.Known {
		t.Fatal("the post-land warm was not visible to the display — the row would read `~`")
	}
	if len(got.Commits) != 0 {
		t.Errorf("post-land verdict = %v, want settled", got.Commits)
	}
	// And the reader got there without the watermark having moved, which is the
	// property that makes the warm worth writing at all.
	if wm, err := f.cache(t).readWatermark(); err != nil || wm.tip != watermarkTip {
		t.Errorf("the watermark moved (%+v, err %v); only the job advances it", wm, err)
	}
}

// TestFreshWorktreeIsSettledWithoutAComparison is the other moment worth warming
// (E-2128): `task claim` cuts a branch AT the base, so the new worktree holds
// nothing the base lacks and the verdict needs no comparison at all.
//
// Both halves matter. That the answer is free is what makes warming it at creation
// free — if this reached `git range-diff` it would put half a second on every
// claim. And that the display then reads it is the point: without it the row for
// the task you just claimed, and are about to look at, shows `~` until the job's
// next pass.
//
// The base is moved AFTER the job pass on purpose, because that is the only shape
// in which the warm is load-bearing. A worktree cut at a tip the job has already
// seen inherits that tip's settled marker for free — the main checkout normally
// sits there, and markers are keyed on the OID alone precisely so two worktrees at
// one tip share one answer. What leaves a gap is the base moving between passes,
// which on a project whose own tooling auto-commits ledger entries and lessons to
// the base branch is most of the time.
func TestFreshWorktreeIsSettledWithoutAComparison(t *testing.T) {
	ctx := context.Background()
	f := newCacheFixture(t, 1)
	f.appendToBase(t, "base.txt")
	f.refresh(t) // a watermark exists, as it does wherever a live view runs
	f.appendToBase(t, "ledger.txt")

	// A worktree exactly as `git worktree add -b <branch> <dir> <base>` leaves it.
	fresh := filepath.Join(f.root, ".endless", "worktrees", "e-9001")
	mustGit(t, f.root, "worktree", "add", "-b", "task/9001", fresh, "main")

	if got := cachedUnlanded(ctx, fresh); got.Known {
		t.Fatalf("setup: a tip the job has not seen cannot have a cached verdict: %+v", got)
	}

	g := countGit(t)
	d := WorktreeUnsettledDetailAt(ctx, fresh)
	if d.Unsettled() {
		t.Errorf("a branch cut at the base is not settled: %s", d.Reason())
	}
	if !d.UnlandedKnown {
		t.Error("the verdict was not established")
	}
	if g.ran("range-diff") {
		t.Errorf("the free answer cost a content comparison: %v", g.calls)
	}

	// And the display reads it, with no job pass in between — through the exact
	// entry point the ◆ column calls, so this is the delay a person would see
	// rather than a property of an inner helper.
	row := WorktreeUnsettledAt(ctx, fresh)
	if !row.UnsettledKnown() {
		t.Error("the row would render `~` after a claim — the creation warm was not visible")
	}
	if row.Unsettled() {
		t.Errorf("a freshly claimed worktree reads as unsettled: %s", row.Reason())
	}
	if !row.UnlandedKnown || row.UnlandedCount != 0 {
		t.Errorf("verdict = %+v, want an established zero", row)
	}
}

// TestWorktreesAtOneTipShareOneAnswer pins the property that made the test above
// need a moved base: a settled marker is keyed on the branch-tip OID and NOTHING
// else, so every worktree standing at that OID reads the same entry.
//
// It is why `settled/` is not nested under the base tip the way `unsettled/` is,
// and it is load-bearing rather than incidental — the main checkout normally sits
// at the base tip, so on a repo the job has swept, a branch cut there is already
// answered before anything warms it.
func TestWorktreesAtOneTipShareOneAnswer(t *testing.T) {
	ctx := context.Background()
	f := newCacheFixture(t, 1)
	f.refresh(t)

	baseTip := mustGit(t, f.root, "rev-parse", "main")
	if got := cachedUnlanded(ctx, f.root); !got.Known || len(got.Commits) != 0 {
		t.Fatalf("the main checkout at the base tip must read settled: %+v", got)
	}

	twin := filepath.Join(f.root, ".endless", "worktrees", "e-9002")
	mustGit(t, f.root, "worktree", "add", "-b", "task/9002", twin, "main")
	if head := mustGit(t, twin, "rev-parse", "HEAD"); head != baseTip {
		t.Fatalf("fixture: the new worktree is at %s, want the base tip %s", head, baseTip)
	}

	g := countGit(t)
	got := cachedUnlanded(ctx, twin)
	if !got.Known || len(got.Commits) != 0 {
		t.Errorf("a second worktree at the same tip did not share the answer: %+v", got)
	}
	for _, forbidden := range []string{"range-diff", "merge-base"} {
		if g.ran(forbidden) {
			t.Errorf("sharing the answer still cost `git %s`: %v", forbidden, g.calls)
		}
	}
}

// TestComputeOnMissReadsTheCacheFirst is the regression E-2128 shipped and had
// to reopen for.
//
// `computeUnlandedAndCache` originally computed unconditionally. That was
// invisible on the two paths that consult the cache themselves before calling it
// — the display probe and the background job — but the REAPER calls it directly
// for condition 4. So every sweep paid the full ~584ms comparison per eligible
// worktree with a warm cache sitting beside it, and because `task spawn` runs the
// reaper, a spawn took about a minute. That is E-2087's regression verbatim: the
// exact cost this whole task exists to remove.
//
// A function named compute-on-MISS must miss before it computes.
func TestComputeOnMissReadsTheCacheFirst(t *testing.T) {
	ctx := context.Background()
	f := newCacheFixture(t, 1)
	f.appendToBase(t, "base.txt")
	f.refresh(t)

	if got := f.read(t, 0); !got.Known {
		t.Fatalf("setup: the job did not establish a verdict: %+v", got)
	}

	g := countGit(t)
	commits, err := computeUnlandedAndCache(ctx, f.worktrees[0], "main")
	if err != nil {
		t.Fatalf("computeUnlandedAndCache: %v", err)
	}
	if len(commits) != 1 {
		t.Errorf("verdict = %v, want the one unlanded commit", commits)
	}
	for _, forbidden := range []string{"range-diff", "merge-base", "rev-list", "log"} {
		if g.ran(forbidden) {
			t.Errorf("a warm cache still cost `git %s` (calls: %v)", forbidden, g.calls)
		}
	}
}

// TestRefreshSkipsADirectoryThatIsNotARepository is the second half of the same
// reopening. `monitor.ProjectRoots` returns every registered project, and not all
// of them are git repositories — one registered project was a plain directory.
// Failing the pass over it raised ERR-0001 "the job failed" once a minute,
// forever, about every OTHER project's sweep as well.
//
// A registered directory that is not a repository has no worktrees and nothing to
// cache, so there is nothing to report.
func TestRefreshSkipsADirectoryThatIsNotARepository(t *testing.T) {
	bindFaultsForTest(t)
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	if err := RefreshUnlandedCache(context.Background(), t.TempDir()); err != nil {
		t.Errorf("a non-repository must be skipped, not fail the sweep: %v", err)
	}
	assertNoIncidents(t)
}

// TestRefreshRecordsAnUnresolvableBaseWithoutFailingTheSweep is the third.
// A repository whose default branch cannot be resolved is a durable property of
// THAT repository, and the fault is fingerprinted on it, so it raises one
// incident with a rising occurrence count. Returning an error as well raised a
// SECOND incident every interval — ERR-0001 on top of ERR-0011 — saying the job
// was broken when one project was.
func TestRefreshRecordsAnUnresolvableBaseWithoutFailingTheSweep(t *testing.T) {
	bindFaultsForTest(t)
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	// A real repository on a branch none of the four resolution steps can name —
	// AND holding a task worktree, without which the pass now skips it before any
	// git call and there is no resolution to fail (see
	// TestRefreshSkipsAProjectWithNoTaskWorktrees).
	repo := fixtureRepo(t, "develop")
	mustGit(t, repo, "worktree", "add", "-b", "task/1",
		filepath.Join(repo, ".endless", "worktrees", "e-1"), "develop")

	if err := RefreshUnlandedCache(context.Background(), repo); err != nil {
		t.Errorf("one project's unresolvable base must not fail the sweep: %v", err)
	}

	incidents := listIncidents(t)
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want exactly 1: %+v", len(incidents), incidents)
	}
	if incidents[0].Code != "ERR-0011" {
		t.Errorf("code = %s, want ERR-0011 — the condition, not a job failure", incidents[0].Code)
	}
}

// TestRefreshSkipsAProjectWithNoTaskWorktrees is the third defect E-2128 shipped,
// found on use after it was marked assumed.
//
// `ProjectRoots` returns every registered project, and the pass swept all of
// them. A project with no task worktrees has nothing to cache — the cache answers
// a strictly per-worktree question — so the work was pure cost. The visible half
// was noise: a registered project that was an EMPTY repository (no commits, so
// `main` is an unborn HEAD resolving to no commit) failed default-branch
// resolution and recorded ERR-0011 every interval, advising the operator to set
// `default_branch` or run `git remote set-head` about a repository whose only
// problem was that nobody had committed to it yet.
//
// The check must come before any git call, which is why the fixtures below assert
// silence rather than a particular error.
func TestRefreshSkipsAProjectWithNoTaskWorktrees(t *testing.T) {
	ctx := context.Background()

	t.Run("empty repository with no commits", func(t *testing.T) {
		bindFaultsForTest(t)
		resetDefaultBranchCache()
		t.Cleanup(resetDefaultBranchCache)

		// Exactly go-diffutils' shape: `git init` and nothing else.
		dir := t.TempDir()
		mustGit(t, dir, "init", "-q", "--initial-branch=main", ".")

		if err := RefreshUnlandedCache(ctx, dir); err != nil {
			t.Errorf("an empty repository must be skipped silently: %v", err)
		}
		assertNoIncidents(t)
	})

	t.Run("healthy repository that simply has no task worktrees", func(t *testing.T) {
		bindFaultsForTest(t)
		resetDefaultBranchCache()
		t.Cleanup(resetDefaultBranchCache)

		repo := fixtureRepo(t, "main")
		if err := RefreshUnlandedCache(ctx, repo); err != nil {
			t.Errorf("a project with no task worktrees must be skipped: %v", err)
		}
		assertNoIncidents(t)
		// And it wrote nothing — no cache directory for a repo with no readers.
		c, err := unlandedCacheFor(ctx, repo)
		if err != nil {
			t.Fatalf("unlandedCacheFor: %v", err)
		}
		if _, serr := os.Stat(c.dir); !os.IsNotExist(serr) {
			t.Errorf("a skipped project got a cache directory: %v", serr)
		}
	})

	t.Run("a project WITH task worktrees is still swept", func(t *testing.T) {
		f := newCacheFixture(t, 1)
		if err := RefreshUnlandedCache(ctx, f.root); err != nil {
			t.Fatalf("RefreshUnlandedCache: %v", err)
		}
		if got := f.read(t, 0); !got.Known {
			t.Errorf("the skip swallowed a project that does have worktrees: %+v", got)
		}
	})
}
