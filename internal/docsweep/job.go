package docsweep

import (
	"context"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
)

// JobName keys this job's scheduling row. It must stay stable: changing it
// orphans the existing row and restarts the job's history from zero.
const JobName = "doc-mirrors"

const (
	// interval is the cadence.
	//
	// Fifteen minutes, which is long for a job, because nothing WAITS on this
	// one. `task update` writes the mirror itself, so a converged repository has
	// nothing for the sweep to do and an unconverged one is repairing a file
	// nobody is about to read. The cost of a pass scales with the number of
	// mirrors — one file read per non-empty document column — so the cadence is
	// the only thing keeping that off a developer's machine every minute.
	interval = 15 * time.Minute

	// leaseTTL bounds the run and doubles as its context deadline.
	//
	// The expensive pass is the FIRST one on an unconverged repository: several
	// hundred relocations plus a backfill of every mirror that only ever existed
	// on a task branch, then one commit over all of them. Ten minutes clears that
	// comfortably while still expiring long before a second scheduled run.
	leaseTTL = 10 * time.Minute
)

// job implements jobs.Job. It carries no state: which repositories to cover
// comes from the projects table, and what to do comes from comparing each
// mirror against its column.
type job struct{}

func init() {
	jobs.Register(job{})
}

// Name returns the stable scheduling key.
func (job) Name() (name string) {
	return JobName
}

// Schedule declares the cadence and the lease.
//
// MaxBackoff is deliberately ZERO — no backoff. internal/jobs names the case
// backoff exists for: jobs whose failures are EXPENSIVE, so a broken one decays
// toward a cap instead of burning spend every interval. This job's failures cost
// some file reads and a git invocation, and they are idempotent. Backing off
// would leave mirrors stale for longer exactly when the write path is known to
// be sick. The fault the runner records is the alarm.
func (job) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval: interval,
		LeaseTTL: leaseTTL,
	}
}

// Run is the sweep.
//
// Idempotency — the lease contract's hard requirement — is structural rather
// than arranged: every decision is a comparison between a file and a column, so
// a re-claimed run finds the first run's work already done and writes nothing.
// Nothing accumulates and nothing is appended to.
func (job) Run(ctx context.Context) (err error) {
	return Run(ctx)
}
