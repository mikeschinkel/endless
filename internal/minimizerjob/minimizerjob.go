// Package minimizerjob is the background tick behind the minimizer's
// autoresearch loop (E-1975).
//
// # What rides here, and why nothing else does
//
// ED-1556 makes the loop something the user never gates: they supply ground
// truth in the flow of work and the loop improves itself around them. That is
// only true if something fires it without being asked, and the fire-once job
// runner is already that something. A dedicated scheduler is a later question,
// if ever — this loop has no latency requirement worth a second scheduling
// mechanism, because nothing waits on its output.
//
// # Why this is a subprocess and not Go
//
// Both halves of the tick call a model: the judge scores a turn, and the
// optimizer generates variants and replays them. Model invocation goes through
// `run_internal_claude`, which is Python and stays Python under E-1486's
// boundary. A Go reimplementation here would be a SECOND model-invocation
// harness in a second language — exactly what E-1849 would then have to unify.
// So this job is thin: it shells the Python CLI and reports what happened.
//
// # Cadence
//
// One interval, not two. Judging is cheap and wants to be prompt (a judgment is
// only a PREDICTION while the user has not reacted yet, so a late sweep silently
// converts the loop's own honesty check into hindsight); replaying is expensive
// and wants to be rare. Rather than register two jobs, the tick judges every
// time and the Python side decides whether an optimize round is due from its own
// persisted timestamp. That keeps the expensive cadence configurable in the
// place that knows what a round costs.
package minimizerjob

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// JobName keys this job's scheduling row. It must stay stable: changing it
// orphans the existing row and restarts the job's history from zero.
const JobName = "minimizer-loop"

const (
	// judgeLimit caps how many corpus rows one tick judges. Every row is a model
	// call, so this is a spend cap as much as a batch size.
	judgeLimit = 8

	// perCallTimeout mirrors the Python side's per-call budget. Used only to
	// derive the lease; the Python side enforces it.
	perCallTimeout = 120 * time.Second

	// replayCalls is the worst-case number of model calls an optimize round adds
	// on top of the judge sweep: corpus slice × (champion + challenger
	// minimization, then a judgment of each). It exists to size the lease, and
	// it must not understate the Python side's own caps.
	replayCalls = 48

	// interval is the tick cadence. Judging is the thing that wants to be
	// prompt, and 10 minutes is well inside the window where a user has not yet
	// reacted to the reply being judged.
	interval = 10 * time.Minute

	// maxBackoff caps exponential backoff after consecutive failures.
	// internal/jobs names model-calling jobs as exactly the case MaxBackoff
	// exists for: a persistently broken loop decays toward this cap instead of
	// burning model spend every interval, and stays visible in `jobs list`
	// rather than going silently dark.
	maxBackoff = 12 * time.Hour

	// leaseHeadroom pads the lease beyond the worst-case tick so a slow but
	// healthy run is never re-claimed underneath itself.
	leaseHeadroom = 15 * time.Minute
)

// job implements jobs.Job. It carries no state: the queue lives in the DB and
// every decision lives in the subprocess.
type job struct{}

func init() {
	jobs.Register(job{})
}

// Name returns the stable scheduling key.
func (job) Name() (name string) {
	return JobName
}

// Schedule declares the tick cadence, the backoff cap, and a lease that
// generously exceeds the worst-case run — a full judge sweep AND an optimize
// round landing on the same tick.
func (job) Schedule() (schedule jobs.Schedule) {
	worst := time.Duration(judgeLimit+replayCalls) * perCallTimeout
	return jobs.Schedule{
		Interval:   interval,
		MaxBackoff: maxBackoff,
		LeaseTTL:   worst + leaseHeadroom,
	}
}

// Run ticks the loop by shelling `endless minimizer run`.
//
// Idempotency — the lease contract's hard requirement — comes from the Python
// side: the judge selects only rows with no judgment and writes one row per
// corpus row under a UNIQUE constraint, and the optimizer claims its round
// through a persisted timestamp. A re-claimed tick therefore judges each row at
// most once and cannot run two rounds for one due window.
//
// stdout and stderr are CAPTURED, never inherited: jobs.Job forbids writing to
// either, because the trigger may be a live TUI painting the same terminal.
// They are folded into the returned error so a failure is still legible in the
// recorded fault.
func (job) Run(ctx context.Context) (err error) {
	var cmd *exec.Cmd
	var out []byte
	var bin, dir string
	var dbArgs []string

	bin, err = exec.LookPath("endless")
	if err != nil {
		err = fmt.Errorf("%w: the Python CLI is not on PATH", err)
		goto end
	}

	// The child opens this process's database by flag, never by environment
	// (E-2186): see monitor.ChildDBRoute for the three cases.
	dbArgs, dir, err = monitor.ChildDBRoute()
	if err != nil {
		err = fmt.Errorf("minimizer tick: %w", err)
		goto end
	}
	cmd = exec.CommandContext(ctx, bin, append(dbArgs,
		"minimizer", "run",
		"--limit", strconv.Itoa(judgeLimit),
	)...)
	cmd.Dir = dir

	out, err = cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("minimizer tick: %w: %s", err, tail(string(out)))
		goto end
	}

end:
	return err
}

// tailLimit bounds how much subprocess output rides along in a fault. The
// recorded detail is for a human triaging a broken loop, not an archive.
const tailLimit = 2000

func tail(s string) (out string) {
	out = strings.TrimSpace(s)
	if len(out) > tailLimit {
		out = "…" + out[len(out)-tailLimit:]
	}
	return out
}
