package docsweep

import (
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// TestRegistered pins the wiring, not the logic: the job's init() must have put
// it in the registry, and cmd/endless-go must blank-import this package. A job
// that compiles but never registers is silently dead — mirrors would simply
// never converge, and nothing else would look wrong.
func TestRegistered(t *testing.T) {
	got, ok := jobs.Lookup(JobName)
	if !ok {
		t.Fatalf("%q is not registered; the runner will never fire it", JobName)
	}
	if got.Name() != JobName {
		t.Errorf("Name() = %q, want %q", got.Name(), JobName)
	}
}

// TestScheduleIsValidAndExplicit pins the two things a wrong Schedule would do
// quietly: a zero Interval is an invalid Schedule outright, and a LeaseTTL left
// to the default would be max(2*Interval, 5m) — half an hour for this job, which
// is longer than the interval and would let a second invocation claim a run
// while the first was still committing.
func TestScheduleIsValidAndExplicit(t *testing.T) {
	s := (job{}).Schedule()
	if s.Interval <= 0 {
		t.Error("Interval must be positive; the zero Schedule is invalid")
	}
	if s.LeaseTTL <= 0 {
		t.Fatal("LeaseTTL must be set explicitly")
	}
	if s.LeaseTTL >= s.Interval {
		t.Errorf("LeaseTTL %s is not shorter than Interval %s, so a slow run "+
			"could still hold its claim when the next one comes due",
			s.LeaseTTL, s.Interval)
	}
	// Idempotent and cheap: no backoff, per internal/jobs' own rule that backoff
	// is for jobs whose failures are expensive.
	if s.MaxBackoff != 0 {
		t.Errorf("MaxBackoff = %s, want 0 for cheap idempotent work", s.MaxBackoff)
	}
	if s.Interval < time.Minute {
		t.Errorf("Interval %s is aggressive for a repair pass nothing waits on",
			s.Interval)
	}
}
