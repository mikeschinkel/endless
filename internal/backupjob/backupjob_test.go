package backupjob

import (
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// TestRegistered pins the wiring, not the logic: the job's init() must have put
// it in the registry. A job that compiles but never registers is silently dead,
// and the only symptom is the thing this task exists to fix — backups that stop
// happening — showing up again weeks later.
func TestRegistered(t *testing.T) {
	got, ok := jobs.Lookup(JobName)
	if !ok {
		t.Fatalf("%q is not registered; the runner will never fire it", JobName)
	}
	if got.Name() != JobName {
		t.Errorf("Name() = %q, want %q", got.Name(), JobName)
	}
}

// TestScheduleIsACadence checks the three numbers that decide whether this job
// does its one job.
func TestScheduleIsACadence(t *testing.T) {
	s := job{}.Schedule()

	if s.Interval <= 0 {
		t.Fatal("Interval must be positive; the zero Schedule is invalid")
	}
	// The data-loss window. An interval coarser than the retention policy's
	// finest tier would leave the hourly tier with gaps it could never fill.
	if s.Interval > time.Hour {
		t.Errorf("Interval %s is coarser than the hourly retention tier", s.Interval)
	}
	// The lease must exceed a realistic VACUUM INTO by a wide margin, or a
	// healthy-but-slow backup gets re-claimed underneath itself.
	if s.LeaseTTL < 5*time.Minute {
		t.Errorf("LeaseTTL %s leaves no headroom over a slow VACUUM INTO", s.LeaseTTL)
	}
	// And it must not exceed the interval: a lease longer than the cadence parks
	// the job for more than one cycle when a process dies mid-run.
	if s.LeaseTTL > s.Interval {
		t.Errorf("LeaseTTL %s exceeds Interval %s; a dead process would park the job",
			s.LeaseTTL, s.Interval)
	}
	// Zero MaxBackoff is a decision, not an oversight — see Schedule's comment.
	// Backing a failing backup off would widen the data-loss window exactly when
	// the backup path is known to be sick.
	if s.MaxBackoff != 0 {
		t.Errorf("MaxBackoff = %s, want 0: a failing backup must keep retrying at Interval",
			s.MaxBackoff)
	}
}

// TestJobNameIsStable guards the one string that must never be edited casually:
// it keys the scheduling row, so changing it orphans the row and restarts the
// job's history — including its next_due_at — from zero.
func TestJobNameIsStable(t *testing.T) {
	if JobName != "db-backup" {
		t.Errorf("JobName = %q; changing it orphans the existing scheduling row", JobName)
	}
}
