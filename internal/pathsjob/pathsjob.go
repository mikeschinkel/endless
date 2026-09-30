// Package pathsjob keeps the task-worktree changed-path cache warm (E-2164).
//
// `session status` draws `E-1 <> E-2` when two open tasks' worktrees touch a
// common path. Finding that out takes a `git diff` and a `git status` per
// worktree, and ED-1589's rule is that a surface repainting on a timer reads a
// value a background job wrote rather than computing it. This is that job; the
// cache and its reader are internal/monitor/worktree_paths_cache.go.
//
// It is internal/unlandedjob's sibling in every respect that matters — Go
// rather than a subprocess, no backoff, idempotent by content keys — and is a
// separate job rather than a second pass inside that one so each keeps its own
// fault identity and schedule row.
package pathsjob

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
const JobName = "worktree-paths"

const (
	// interval bounds how late an uncommitted edit reaches the graph. A warm
	// pass is one `git status` per OPEN task's worktree plus a file read; only a
	// moved HEAD or a changed working tree costs a diff.
	interval = time.Minute

	// leaseTTL bounds the run and doubles as its context deadline, set
	// explicitly for unlandedjob's reason: the default would be five minutes,
	// thin headroom for a cold pass over a large repository.
	leaseTTL = 15 * time.Minute
)

type job struct{}

func init() {
	jobs.Register(job{})
}

// Name returns the stable scheduling key.
func (job) Name() (name string) {
	return JobName
}

// Schedule declares the cadence and the lease. No backoff, for the reason
// internal/unlandedjob gives: failures are cheap and idempotent, and backing off
// would leave the cache stale exactly when it is known to be sick.
func (job) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval: interval,
		LeaseTTL: leaseTTL,
	}
}

// Run refreshes every registered project's cache for the open tasks. One
// project's failure does not abandon the others; the failures are returned
// together so the runner records one fault naming all of them.
func (job) Run(ctx context.Context) (err error) {
	var roots []string
	var open map[int64]bool
	var failures []string

	roots, err = monitor.ProjectRoots()
	if err != nil {
		err = fmt.Errorf("enumerate projects: %w", err)
		goto end
	}
	open, err = monitor.OpenTaskIDs()
	if err != nil {
		err = fmt.Errorf("enumerate open tasks: %w", err)
		goto end
	}

	for _, root := range roots {
		if rerr := monitor.RefreshWorktreePathsCache(ctx, root, open); rerr != nil {
			failures = append(failures, rerr.Error())
		}
		if ctx.Err() != nil {
			break
		}
	}

	if len(failures) > 0 {
		err = errors.New(strings.Join(failures, "; "))
	}

end:
	return err
}
