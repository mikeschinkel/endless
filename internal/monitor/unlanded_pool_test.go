package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// E-2128 — the bounded pool, and the cancellation it exists to make possible.

// TestPoolBoundsConcurrencyOverAColdBatch is the cap, stated as a test. A cold
// batch over N worktrees must complete in full while never having more than
// unlandedPoolSize() git children in flight: git is CPU-bound on patch-ids here,
// and a pass that forked one child per worktree would take the machine with it.
func TestPoolBoundsConcurrencyOverAColdBatch(t *testing.T) {
	const worktrees = 40

	var mu sync.Mutex
	inFlight, peak := 0, 0

	prev := runGit
	t.Cleanup(func() { runGit = prev })
	runGit = func(_ context.Context, dir string, args ...string) (string, error) {
		// Only the comparison's own commands are pool work; the cache's path
		// lookups are not, and counting them would measure the wrong thing.
		switch args[0] {
		case "merge-base", "rev-list", "range-diff", "log":
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			// Long enough that every queued item has a chance to pile up behind the
			// semaphore, short enough to keep the test quick.
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
		}
		switch args[0] {
		case "merge-base":
			return "b45e0000\n", nil
		case "rev-list":
			return "2\n", nil
		case "range-diff":
			return stubRangeDiff(1), nil
		case "rev-parse":
			return "deadbeef\n", nil
		}
		return "", nil
	}

	reqs := make([]unlandedRequest, worktrees)
	for i := range reqs {
		reqs[i] = unlandedRequest{Dir: "/wt/e-" + strconv.Itoa(i), Base: "main"}
	}
	out := computeUnlandedBatch(context.Background(), reqs)

	if len(out) != worktrees {
		t.Fatalf("got %d outcomes, want %d", len(out), worktrees)
	}
	for i, o := range out {
		if o.Err != nil {
			t.Errorf("outcome %d failed: %v", i, o.Err)
		}
		if o.Dir != reqs[i].Dir {
			t.Errorf("outcome %d is for %q, want %q — the pool must preserve order",
				i, o.Dir, reqs[i].Dir)
		}
	}

	mu.Lock()
	got := peak
	mu.Unlock()
	if cap := unlandedPoolSize(); got > cap {
		t.Errorf("peak concurrency %d exceeded the pool cap %d", got, cap)
	}
	if got < 2 && unlandedPoolSize() > 1 {
		t.Errorf("peak concurrency %d — the batch ran serially, so the cap proves nothing", got)
	}
}

// TestPoolSizeIsBoundedAndPositive pins both ends of the sizing rule. The ceiling
// is the refusal to fork 32 range-diffs from a background job on a 32-core
// machine; the floor is that a pool of zero does no work at all.
func TestPoolSizeIsBoundedAndPositive(t *testing.T) {
	n := unlandedPoolSize()
	if n < 1 {
		t.Errorf("unlandedPoolSize() = %d, want at least 1", n)
	}
	if n > 8 {
		t.Errorf("unlandedPoolSize() = %d, want at most 8", n)
	}
}

// TestCancelledProbeIsInterruptedAndRecordsNoFault is the guarantee E-2113 landed
// for SIGINT, extended to the cancellation a job lease now performs (E-2128).
//
// The trap this covers: exec.CommandContext kills with SIGKILL, and E-2113
// deliberately keeps SIGKILL faultable, because a SIGKILL is usually the OOM
// killer and a probe big enough to be OOM-killed is a real operational fact. So
// cancelling a lease would have recorded one ERR-0010 per in-flight worktree —
// naming innocent tasks — unless the cancellation is recognised by the CONTEXT
// rather than by the signal. That is what runGit does, and this is the test that
// says so.
func TestCancelledProbeIsInterruptedAndRecordsNoFault(t *testing.T) {
	bindFaultsForTest(t)
	// A git that never answers, so the cancellation is what ends it.
	writeGitShim(t, "sleep 30")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := runGit(ctx, t.TempDir(), "range-diff", "--no-patch", "a..b", "a..c")
	if !errors.Is(err, ErrGitInterrupted) {
		t.Fatalf("runGit err = %v, want it to satisfy errors.Is(err, ErrGitInterrupted)", err)
	}

	// And the guard on the recorder holds for this class as it does for SIGINT: a
	// probe that was killed established nothing, so there is nothing to report.
	recordProbeFault(UnsettledDetail{WorktreePath: "/wt/e-2128"}, "git range-diff", err.Error(), err)
	assertNoIncidents(t)
}

// TestCancellingAPassStopsItWithoutFaults is the same guarantee one level up,
// through the pool: a lease that expires mid-pass must leave no incident behind,
// however many worktrees were in flight.
func TestCancellingAPassStopsItWithoutFaults(t *testing.T) {
	bindFaultsForTest(t)

	// A real repo, so the cache resolution and watermark read are real; the shim
	// below only replaces the comparison.
	root := fixtureRepo(t, "main")
	resetDefaultBranchCache()
	t.Cleanup(resetDefaultBranchCache)

	var dirs []string
	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, ".endless", "worktrees", "e-"+strconv.Itoa(4000+i))
		mustGit(t, root, "worktree", "add", "-b", "task/"+strconv.Itoa(4000+i), dir)
		if err := os.WriteFile(filepath.Join(dir, "w.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, dir, "add", "w.txt")
		mustGit(t, dir, "commit", "-m", "work")
		dirs = append(dirs, dir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	prev := runGit
	t.Cleanup(func() { runGit = prev })
	var once sync.Once
	runGit = func(c context.Context, dir string, args ...string) (string, error) {
		// Cancel as soon as the first comparison starts, so the pass is stopped
		// with work still queued.
		if args[0] == "merge-base" || args[0] == "range-diff" {
			once.Do(cancel)
		}
		return prev(c, dir, args...)
	}

	reqs := make([]unlandedRequest, 0, len(dirs))
	for _, d := range dirs {
		reqs = append(reqs, unlandedRequest{Dir: d, Base: "main"})
	}
	computeUnlandedBatch(ctx, reqs)

	assertNoIncidents(t)
}
