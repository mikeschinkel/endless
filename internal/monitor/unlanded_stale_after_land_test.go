package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// E-2277 — a just-landed worktree must read settled the moment the land
// finishes, not one job interval later.
//
// The window: the branch is rebased onto the base, something computes its
// verdict under the CURRENT watermark (writing unsettled/<wm-tip>/<head>), and
// then the base is fast-forwarded to that same head. The branch tip did not
// move, the watermark did not move, so before E-2277 the stale entry stayed
// reachable — and the post-land warm, which is a compute-on-miss read, returned
// it as a hit instead of computing. `task unsettled` then said "unlanded (1
// commit)" with the remedy "worktree land", for a task that had just landed.

// staleUnsettledAfterLand builds that exact state against real git and returns
// the worktree directory and its head.
func staleUnsettledAfterLand(t *testing.T) (*cacheFixture, string, string) {
	t.Helper()
	f := newCacheFixture(t, 1)
	f.appendToBase(t, "base.txt")
	f.refresh(t) // the watermark now names the base tip
	wmTip := mustGit(t, f.root, "rev-parse", "main")

	wt := f.worktrees[0]
	mustGit(t, wt, "rebase", "main")
	head := mustGit(t, wt, "rev-parse", "HEAD")

	// What any compute-mode reader writes between the rebase and the ff-merge.
	if err := f.cache(t).writeEntry(wmTip, head, []string{"abc1234 E-2129: the work"}); err != nil {
		t.Fatalf("seed stale entry: %v", err)
	}

	mustGit(t, f.root, "merge", "--ff-only", f.branches[0])

	if got := f.read(t, 0); !got.Known || len(got.Commits) != 1 {
		t.Fatalf("setup: the stale unsettled entry should still be readable: %+v", got)
	}
	return f, wt, head
}

func TestJustLandedWorktreeReadsSettledOnDemand(t *testing.T) {
	ctx := context.Background()
	f, wt, head := staleUnsettledAfterLand(t)

	g := countGit(t)
	d := WorktreeUnsettledDetailAt(ctx, wt)
	if d.Unsettled() {
		t.Fatalf("a just-landed worktree reads unsettled: %s", d.Reason())
	}
	if !d.UnlandedKnown || d.UnlandedCount != 0 {
		t.Errorf("verdict = %+v, want an established zero", d)
	}
	if g.ran("range-diff") {
		t.Errorf("an ancestor of the base cost a content comparison: %v", g.calls)
	}
	if _, err := os.Stat(f.cache(t).settledPath(head)); err != nil {
		t.Errorf("settled/<head> was not written: %v", err)
	}

	// And the display — cache-only, no job pass in between — now agrees.
	if got := f.read(t, 0); !got.Known || len(got.Commits) != 0 {
		t.Errorf("the ◆ read after the on-demand answer = %+v, want settled", got)
	}
}

func TestComputeOnMissDropsAStaleUnsettledHitForAnAncestor(t *testing.T) {
	_, wt, _ := staleUnsettledAfterLand(t)
	commits, err := computeUnlandedAndCache(context.Background(), wt, "main")
	if err != nil {
		t.Fatalf("computeUnlandedAndCache: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("verdict = %v, want settled — the reaper would skip a landed worktree", commits)
	}
}

// TestUnsettledHitForANonAncestorIsStillTrusted pins the other half: a branch
// that is NOT contained in the base keeps answering from its cached entry, with
// no comparison — the ancestor check adds one cheap call, never the range-diff.
func TestUnsettledHitForANonAncestorIsStillTrusted(t *testing.T) {
	ctx := context.Background()
	f := newCacheFixture(t, 1)
	f.appendToBase(t, "base.txt")
	f.refresh(t)
	wmTip := mustGit(t, f.root, "rev-parse", "main")
	head := mustGit(t, f.worktrees[0], "rev-parse", "HEAD")

	// A sentinel line the real comparison would never produce, so a recompute
	// would be visible in the answer itself.
	sentinel := []string{"0000000 sentinel: from the cache"}
	if err := f.cache(t).writeEntry(wmTip, head, sentinel); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	g := countGit(t)
	d := WorktreeUnsettledDetailAt(ctx, f.worktrees[0])
	if !d.Unsettled() || d.UnlandedCount != 1 || len(d.UnlandedLog) != 1 || d.UnlandedLog[0] != sentinel[0] {
		t.Errorf("verdict = %+v, want the cached entry as-is", d)
	}
	if g.ran("range-diff") || g.ran("rev-list") {
		t.Errorf("a warm unsettled hit was recomputed: %v", g.calls)
	}
	if _, err := os.Stat(filepath.Join(f.cache(t).dir, settledSubdir, head)); err == nil {
		t.Error("a branch with unlanded work was marked settled")
	}
}
