// Package backupjob keeps a recent database backup on the shelf (E-2121).
//
// # Why this exists
//
// Backups used to happen only before a schema migration. `monitor.BackupDB` had
// two callers — the pre-apply step of a land, and an explicit `endless db
// backup` — and neither is a schedule. So the newest backup was as old as the
// last schema change: nine days, on the day one was needed to undo a bulk edit.
// It was still usable, but only by luck, because nothing in that design made
// recency likely.
//
// The 60-second window inside BackupDB reads like a cadence and is not one. It
// is a throttle, stopping two migrations seconds apart from writing two copies.
// A throttle can only ever prevent a backup; something has to CAUSE one. This
// job is that something.
//
// # Why Go and not a subprocess
//
// The other two jobs (internal/triagejob, internal/minimizerjob) shell out to
// the Python CLI because their work is a model call, and model invocation is
// Python under E-1486's boundary. Nothing here calls a model: the backup is
// SQLite's VACUUM INTO and the retention sweep is a directory listing, both
// already in this binary. A subprocess would buy a fork and lose the context
// deadline.
//
// # Why the machine's backup software is not the answer
//
// A filesystem snapshot of a live SQLite database has no consistency guarantee,
// and this is not theoretical: a Time Machine copy taken mid-write failed
// `PRAGMA integrity_check`, and `.recover` reassembled it wrongly — fields from
// one table landing in another's columns, whole rows missing. Endless's own
// backups go through VACUUM INTO, which is transactional. That difference is
// why the schedule belongs here.
package backupjob

import (
	"context"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// JobName keys this job's scheduling row. It must stay stable: changing it
// orphans the existing row and restarts the job's history from zero.
const JobName = "db-backup"

const (
	// interval is the backup cadence, and it is the whole point of this job.
	//
	// Unlike the model-calling jobs, this one trades against nothing but disk:
	// a backup costs a VACUUM INTO and a few megabytes. So the number is chosen
	// purely as an acceptable data-loss window — an hour — and it is the same
	// hour the retention policy's finest tier keeps, so a steady state of one
	// backup per hour for the last day falls straight out.
	interval = time.Hour

	// leaseTTL bounds the run and doubles as its context deadline. VACUUM INTO
	// on a database of this size is seconds; fifteen minutes is generous by
	// three orders of magnitude and still well under the interval, so a process
	// that dies mid-backup does not park the job for the rest of the hour.
	//
	// Set explicitly rather than defaulted: the default is max(2*Interval, 5m),
	// which for an hourly job is TWO HOURS of un-reclaimable lease.
	leaseTTL = 15 * time.Minute
)

// job implements jobs.Job. It carries no state: what to back up and where comes
// from the resolved database path, and the retention policy reads the directory
// it prunes.
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
// MaxBackoff is deliberately ZERO — no backoff. internal/jobs names the case it
// exists for: jobs whose failures are EXPENSIVE, so a broken one decays toward a
// cap instead of burning spend every interval. This job's failures cost a failed
// VACUUM, and backing a failing backup off toward a six-hour cap would widen the
// data-loss window exactly when the backup path is known to be sick. Retrying
// every hour is the right behaviour for cheap idempotent work, and the fault the
// runner records is what tells the operator.
func (job) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval: interval,
		LeaseTTL: leaseTTL,
	}
}

// Run writes a backup and enforces the retention policy.
//
// Idempotency — the lease contract's hard requirement — is free here. The
// backup's name carries a whole-second timestamp and BackupDB throttles on the
// newest existing backup, so a re-claimed run inside the throttle window writes
// nothing and reports the copy already on disk. Retention is a pure function of
// the directory's contents and the clock: running it twice removes nothing the
// first pass did not.
//
// A retention failure fails the run. BackupDB folds both halves into one error
// and the distinction a CLI needs — "the copy is on disk, only the sweep failed"
// — is not a distinction a scheduled job should make: a backups directory that
// has quietly stopped being pruned is precisely the kind of slow rot that only
// gets noticed if something reports it, and the runner's fault record is that
// something.
func (job) Run(ctx context.Context) (err error) {
	_, err = monitor.BackupDBContext(ctx)
	return err
}
