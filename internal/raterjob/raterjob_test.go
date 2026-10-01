package raterjob

import (
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// TestRegistered pins the wiring, not the logic: the job's init() must have put
// it in the registry. A job that compiles but never registers is silently dead.
func TestRegistered(t *testing.T) {
	got, ok := jobs.Lookup(JobName)
	if !ok {
		t.Fatalf("%q is not registered; the runner will never fire it", JobName)
	}
	if got.Name() != "rater" {
		t.Errorf("Name() = %q, want %q", got.Name(), "rater")
	}
}

// TestScheduleLeaseExceedsWorstCase is the lease contract from internal/jobs:
// once the lease expires another invocation may claim and run the job
// CONCURRENTLY, so the lease must exceed batchLimit sequential model calls.
func TestScheduleLeaseExceedsWorstCase(t *testing.T) {
	s := job{}.Schedule()
	worst := time.Duration(batchLimit) * perTaskTimeout
	if s.LeaseTTL <= worst {
		t.Errorf("LeaseTTL %s does not exceed worst-case sweep %s", s.LeaseTTL, worst)
	}
	if s.Interval <= 0 {
		t.Error("Interval must be positive; the zero Schedule is invalid")
	}
	// A model-calling job without real backoff burns spend every Interval
	// forever once it breaks.
	if s.MaxBackoff <= s.Interval {
		t.Errorf("MaxBackoff %s must exceed Interval %s to actually back off",
			s.MaxBackoff, s.Interval)
	}
}

// TestRunArgsSweepEveryProjectCapped: the job has a database but no cwd, so the
// sweep must be database-wide, and the limit must be the one the lease above
// was sized from.
func TestRunArgsSweepEveryProjectCapped(t *testing.T) {
	got := strings.Join(runArgs(), " ")
	want := "rater run --all-projects --limit 10"
	if got != want {
		t.Errorf("runArgs() = %q, want %q", got, want)
	}
}

func TestSummaryIsTheLastLine(t *testing.T) {
	if got := summary("• E-1 rated\n  E-2 skipped\nrated 1 of 2\n"); got != "rated 1 of 2" {
		t.Errorf("summary = %q", got)
	}
}

func TestTailBoundsFaultDetail(t *testing.T) {
	long := strings.Repeat("x", tailLimit*2)
	got := tail(long)
	if len(got) > tailLimit+len("…") {
		t.Errorf("tail returned %d bytes, want <= %d", len(got), tailLimit+len("…"))
	}
	if tail("  hi  ") != "hi" {
		t.Errorf("tail did not trim: %q", tail("  hi  "))
	}
}
