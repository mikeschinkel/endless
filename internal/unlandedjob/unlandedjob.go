// Package unlandedjob keeps the unlanded-verdict cache warm (E-2128, ED-1589).
//
// # Why this exists
//
// "Does this branch hold work the base branch lacks?" is answered exactly by
// `git range-diff`, at about 584ms per worktree. Every consumer used to compute
// it for itself, on every tick: the reaper on five Claude hook branches including
// PreToolUse and PostToolUse, and every `session monitor` pane on every rendered
// row every two seconds. The same half-second therefore became a continuous
// multi-core load that multiplied with each pane opened, and on the tool-call
// path it had already made a sweep cost ninety seconds.
//
// ED-1589's rule is that a surface repainting on a timer reads a value a
// background job wrote. This is that job. It is the only writer of the watermark
// in monitor's cache (internal/monitor/unlanded_cache.go), and the displays are
// read-only against it.
//
// # Why Go and not a subprocess
//
// The same reason internal/backupjob is Go: nothing here calls a model. The work
// is `git` and a few file writes, both already in this binary, and the two
// model-calling jobs shell out to Python only because model invocation is
// Python's under E-1486's boundary. A subprocess would buy a fork and lose the
// lease's context deadline — which this job needs more than most, because the
// deadline is what stops its in-flight git children.
//
// # Why the reaper is not also a job
//
// It already runs where it should. `worktree land` sweeps after a successful
// land, shelling to `endless-go event reap-worktrees` with stderr forwarded, so
// reaped directories are printed to the person who ran the land. A land is what
// makes other worktrees reclaimable, and the person who caused the reclamation is
// the one who should see it. E-2128 deleted the hook copies and added no cadence.
package unlandedjob

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// JobName keys this job's scheduling row. It must stay stable: changing it
// orphans the existing row and restarts the job's history from zero.
const JobName = "worktree-unlanded"

const (
	// interval is the cadence, and it is what bounds how long a `~` — "not yet
	// determined" — can stay on a row whose branch tip just moved.
	//
	// One minute, which is short for a job, because a WARM pass is nearly free:
	// one `git worktree list --porcelain` (measured at 14ms for 135 worktrees on
	// this repo) plus one path lookup per worktree. Only a cold pass or a moved
	// branch tip costs anything, and both are exactly the moments somebody is
	// waiting for the answer.
	interval = time.Minute

	// leaseTTL bounds the run and doubles as its context deadline.
	//
	// A cold pass over 135 worktrees is ~10-20s through the bounded pool; a warm
	// one is milliseconds. Fifteen minutes clears the cold case by two orders of
	// magnitude while staying well inside what a wedged git child could cost, and
	// it is set explicitly because the default — max(2*Interval, 5m) — would be
	// five minutes for a one-minute job, which is not enough headroom for a cold
	// pass on a repository several times this one's size.
	leaseTTL = 15 * time.Minute
)

// job implements jobs.Job. It carries no state: which repositories to cover comes
// from the projects table, and what to recompute comes from comparing the cache
// against git.
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
// MaxBackoff is deliberately ZERO — no backoff — matching internal/backupjob's
// rationale. internal/jobs names the case backoff exists for: jobs whose failures
// are EXPENSIVE, so a broken one decays toward a cap instead of burning spend
// every interval. This job's failures cost a git invocation and are idempotent,
// and backing off would leave the cache stale exactly when the probe path is known
// to be sick — which is when every display would be showing `~`. Retrying every
// interval is right for cheap idempotent work; the fault the runner records is
// the alarm.
func (job) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval: interval,
		LeaseTTL: leaseTTL,
	}
}

// Run refreshes every registered project's cache.
//
// Idempotency — the lease contract's hard requirement — is structural rather than
// arranged: every cache key is a git object id, so a re-claimed run recomputes
// the same verdicts and rewrites the same paths. Nothing accumulates and nothing
// is appended to.
//
// One project's failure does not abandon the others: the repositories are
// independent, and a single unresolvable default branch must not stop every other
// project's cache from being maintained. The failures are collected and returned
// together, so the runner records one fault naming all of them rather than the
// first one it met.
func (job) Run(ctx context.Context) (err error) {
	var roots []string
	var failures []string

	roots, err = monitor.ProjectRoots()
	if err != nil {
		err = fmt.Errorf("enumerate projects: %w", err)
		goto end
	}

	for _, root := range roots {
		if rerr := monitor.RefreshUnlandedCache(ctx, root); rerr != nil {
			failures = append(failures, rerr.Error())
		}
		if ctx.Err() != nil {
			// The lease expired or the process is going down. Stopping here is not a
			// failure: the pass is resumable by construction, because it recomputes
			// only what the cache is missing.
			break
		}
	}

	if len(failures) > 0 {
		err = errors.New(strings.Join(failures, "; "))
	}

end:
	return err
}
