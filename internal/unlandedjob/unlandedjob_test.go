package unlandedjob

import (
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// TestRegistered pins the wiring, not the logic: the job's init() must have put
// it in the registry, and cmd/endless-go must blank-import this package. A job
// that compiles but never registers is silently dead — the cache would simply
// never be written, every row would show `~` forever, and nothing else would look
// wrong.
func TestRegistered(t *testing.T) {
	got, ok := jobs.Lookup(JobName)
	if !ok {
		t.Fatalf("%q is not registered; the runner will never fire it", JobName)
	}
	if got.Name() != JobName {
		t.Errorf("Name() = %q, want %q", got.Name(), JobName)
	}
}

// TestScheduleLeaseExceedsAColdPass is the lease contract from internal/jobs:
// once the lease expires another invocation may claim and run the job
// CONCURRENTLY. A cold pass over this repository's ~135 worktrees is 10-20s
// through the bounded pool, so the lease has to clear that by a wide margin —
// wide enough that a repository several times the size still finishes inside it.
func TestScheduleLeaseExceedsAColdPass(t *testing.T) {
	s := (job{}).Schedule()
	if s.Interval <= 0 {
		t.Error("Interval must be positive; the zero Schedule is invalid")
	}
	const observedColdPass = 20 * time.Second
	if s.LeaseTTL < 10*observedColdPass {
		t.Errorf("LeaseTTL %s leaves too little headroom over a %s cold pass",
			s.LeaseTTL, observedColdPass)
	}
	// Set explicitly rather than defaulted. The default is max(2*Interval, 5m),
	// which for a one-minute job is five minutes — not enough for a cold pass on a
	// repository much larger than this one.
	if s.LeaseTTL <= 2*s.Interval {
		t.Errorf("LeaseTTL %s is no better than the derived default for Interval %s",
			s.LeaseTTL, s.Interval)
	}
}

// TestNoBackoff is a decision, not an omission, so it is asserted rather than
// left to the comment. internal/jobs names the case MaxBackoff exists for: jobs
// whose failures are EXPENSIVE. This job's failures cost a git invocation and are
// idempotent, and backing off would leave the cache stale exactly when the probe
// path is known to be sick — which is precisely when every display is showing `~`.
func TestNoBackoff(t *testing.T) {
	if got := (job{}).Schedule().MaxBackoff; got != 0 {
		t.Errorf("MaxBackoff = %s, want 0 — see internal/backupjob for the same call", got)
	}
}

// TestIntervalIsShortEnoughToBoundTheUnknownWindow is the one scheduling property
// that is about correctness rather than cost.
//
// The interval IS the worst case for how long a row can read "not yet determined"
// after its branch tip moves. A warm pass is one `git worktree list` plus a path
// lookup per worktree, so there is nothing to trade against here — a slow tick
// would buy nothing and cost the display its accuracy.
func TestIntervalIsShortEnoughToBoundTheUnknownWindow(t *testing.T) {
	if got := (job{}).Schedule().Interval; got > 2*time.Minute {
		t.Errorf("Interval %s leaves `~` on the screen too long after a commit", got)
	}
}
