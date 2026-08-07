// Package jobs is the fire-once background job runner (E-698).
//
// When invoked it executes any DUE jobs from the registry, then returns. It has
// no loop and no timer of its own: repetition and scheduling live in the
// TRIGGER, not the runner. Today the trigger is the session monitor, which fires
// RunDue on each refresh; tomorrow a long-running daemon (E-1848) will fire it
// on events, including clock ticks.
//
// # The runner knows nothing job-specific
//
// Jobs are defined by their own tasks and register themselves here. The runner
// only ever sees Name, Schedule and Run. Registration is by blank import in
// cmd/endless-go/main.go, so both triggers in that binary see one registry.
// E-1859 (the description-sufficiency triager, internal/triagejob) is the first
// real client; E-1881 (worktree auto-merge) is the next.
//
// # Concurrency
//
// Many session monitors may fire the runner simultaneously. Exactly one
// invocation runs a given due job, enforced by a DB ticker plus a
// compare-and-set lease:
//
//   - DB ticker: every due/expiry comparison uses SQLite's clock, never Go's, so
//     N racing processes share one clock and cannot disagree about what is due.
//   - CAS lease: claiming is a single conditional UPDATE whose WHERE clause IS
//     the mutual exclusion. RowsAffected == 1 means this invocation owns the job;
//     0 means someone else won, or it was not due.
//
// The lease is time-boxed rather than an OS lock, so a process that dies mid-run
// needs no cleanup — its claim lapses and the next invocation re-claims. The
// corollary is a REQUIREMENT ON JOBS: a merely slow job can be re-claimed once
// its lease expires, so every job must be idempotent, and LeaseTTL must
// generously exceed the job's expected runtime.
//
// # Robustness
//
// The trigger is a live TUI. RunDue must never take it down, so: every job runs
// behind a recover(); every failure is recorded as a fault rather than returned;
// and the standard logger is redirected for the duration of the run, because
// log.Printf writes to stderr while the monitor paints stdout with cursor-home
// escapes — an unredirected log line lands on top of the drawn table and, since
// the monitor only repaints when the frame changes, stays there.
package jobs

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Schedule declares when a job runs and how it backs off. The zero value is
// invalid: Interval must be positive.
//
// Fields beyond these will be added when a real job needs them — the shape is
// deliberately minimal rather than speculative.
type Schedule struct {
	// Interval is the normal cadence: the delay from one completion to the next
	// time the job becomes due.
	Interval time.Duration

	// MaxBackoff caps exponential backoff after consecutive failures. Zero means
	// NO backoff — a failing job simply retries at Interval, which is the right
	// behavior for cheap idempotent work. Set it on jobs whose failures are
	// expensive (a job that calls a model, say) so a persistently broken one
	// decays toward the cap instead of burning the cost every interval. Backoff
	// resets on the first success.
	MaxBackoff time.Duration

	// LeaseTTL bounds how long this job may hold its claim, and doubles as the
	// job's context deadline. Zero defaults to max(2*Interval, 5m). It must
	// exceed the job's realistic worst-case runtime: once it expires another
	// invocation may claim and run the job concurrently.
	LeaseTTL time.Duration
}

// leaseTTL returns the effective lease duration, applying the default.
func (s Schedule) leaseTTL() (ttl time.Duration) {
	ttl = s.LeaseTTL
	if ttl > 0 {
		goto end
	}
	ttl = 2 * s.Interval
	if ttl < defaultMinLeaseTTL {
		ttl = defaultMinLeaseTTL
	}

end:
	return ttl
}

// nextDelay returns the delay until a job with failCount consecutive failures
// becomes due again. failCount is the count AFTER the run being recorded, so 0
// means the run succeeded.
//
// Without MaxBackoff the delay is always Interval. With it, the delay doubles
// per consecutive failure and is capped, so a continuously failing job decays to
// the cap — an effective auto-disable that never goes silently dark, because the
// row keeps a visible next_due_at and fail_count.
func (s Schedule) nextDelay(failCount int) (delay time.Duration) {
	var shift int

	delay = s.Interval
	if failCount <= 0 || s.MaxBackoff <= 0 {
		goto end
	}

	shift = failCount - 1
	if shift > maxBackoffShift {
		delay = s.MaxBackoff
		goto end
	}

	delay = s.Interval * (1 << uint(shift))
	if delay > s.MaxBackoff || delay <= 0 {
		delay = s.MaxBackoff
	}

end:
	return delay
}

const (
	// defaultMinLeaseTTL floors the derived lease so a very short-interval job
	// does not get a lease too brief to finish under load.
	defaultMinLeaseTTL = 5 * time.Minute

	// maxBackoffShift bounds the doubling exponent so the multiplication cannot
	// overflow time.Duration before the MaxBackoff cap is applied.
	maxBackoffShift = 30
)

// Job is one unit of background work. Implementations live in their own
// packages and register here; this package never learns what any of them do.
//
// Run MUST be idempotent — see the lease discussion in the package doc — and
// SHOULD honor ctx, which carries the lease deadline. Run must not write to
// stdout or stderr: the trigger may be a live TUI. Report diagnostics by
// returning an error, or record a fault directly for anything worth surfacing
// on a successful run.
type Job interface {
	Name() string
	Schedule() Schedule
	Run(ctx context.Context) error
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Job)
)

// Register adds a job to the registry. It panics on a duplicate or invalid
// name: registration happens at process start from static code, so a collision
// is a programming error that must never reach a running system, and there is no
// sensible runtime recovery from two jobs claiming one scheduling row.
func Register(job Job) {
	name := job.Name()
	if name == "" {
		panic(ErrRegistering.Error() + ": " + ErrEmptyName.Error())
	}

	registryMu.Lock()
	defer registryMu.Unlock()

	_, dup := registry[name]
	if dup {
		panic(ErrRegistering.Error() + ": " + ErrDuplicateJob.Error() + ": " + name)
	}
	registry[name] = job
}

// Registered returns every registered job, ordered by name so that listings and
// run order are deterministic.
func Registered() (jobs []Job) {
	registryMu.RLock()
	jobs = make([]Job, 0, len(registry))
	for _, job := range registry {
		jobs = append(jobs, job)
	}
	registryMu.RUnlock()

	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].Name() < jobs[j].Name()
	})
	return jobs
}

// Lookup returns the registered job with the given name. ok is false when no
// such job is registered — which is the normal case for a scheduling row left
// behind by a job that has since been removed.
func Lookup(name string) (job Job, ok bool) {
	registryMu.RLock()
	job, ok = registry[name]
	registryMu.RUnlock()
	return job, ok
}

// resetRegistryForTest clears the registry and returns a restore func. Tests
// only: the registry is process-global static wiring in production.
func resetRegistryForTest() (restore func()) {
	registryMu.Lock()
	prev := registry
	registry = make(map[string]Job)
	registryMu.Unlock()

	return func() {
		registryMu.Lock()
		registry = prev
		registryMu.Unlock()
	}
}
