// Package raterjob is the background sweep that proposes complexity and risk
// for `submitted` tasks nobody rated (E-2203, under E-1812).
//
// # Who it is for
//
// Ratings are the agent's to know and the user's to ratify (ED-1538). An agent
// that attaches a plan is refused the promotion to `submitted` until it rates
// the task, so the tasks this job finds are, in practice, ones a PERSON filed
// with a plan — and asking that person to originate the ratings is exactly
// what the design rules out. The job originates them instead, and the person
// ratifies them at `task approve` as usual.
//
// # Why this is a subprocess and not Go
//
// The model call goes through `run_internal_claude`, which is Python and stays
// Python under E-1486's boundary. A Go reimplementation here would be a SECOND
// model-invocation harness in a second language. So this job is thin, as the
// retired triage job it is modelled on was (E-1859, removed by E-1993): it
// shells `endless rater run` and reports what happened. The decision logic
// lives in src/endless/rater.py.
package raterjob

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
const JobName = "rater"

const (
	// batchLimit caps one sweep. Every selected task becomes a model call, so
	// this is a spend cap as much as a batch size. It mirrors
	// rater.DEFAULT_BATCH_LIMIT; it is passed explicitly rather than left to
	// the Python default so the lease arithmetic below derives from a number
	// this file can see.
	batchLimit = 10

	// perTaskTimeout mirrors rater.CALL_TIMEOUT_SECONDS. Used only to derive
	// the lease; the Python side enforces it.
	perTaskTimeout = 120 * time.Second

	// interval is the sweep cadence. It does not trade against model spend:
	// the sweep selects unrated `submitted` rows first, so an empty queue costs
	// one query. Spend is proportional to tasks filed, not to polling.
	interval = 5 * time.Minute

	// maxBackoff caps exponential backoff after consecutive failures.
	// internal/jobs names model-calling jobs as exactly the case MaxBackoff
	// exists for: a persistently broken rater decays toward this cap instead of
	// burning model spend every interval.
	maxBackoff = 6 * time.Hour

	// leaseHeadroom pads the lease beyond the worst-case sweep so a slow but
	// healthy run is never re-claimed underneath itself.
	leaseHeadroom = 10 * time.Minute
)

// job implements jobs.Job. It carries no state: the queue lives in the DB and
// the decision logic lives in the subprocess.
type job struct{}

func init() {
	jobs.Register(job{})
}

// Name returns the stable scheduling key.
func (job) Name() (name string) {
	return JobName
}

// Schedule declares the sweep cadence, the backoff cap, and a lease that
// generously exceeds the worst-case run (batchLimit sequential model calls).
func (job) Schedule() (schedule jobs.Schedule) {
	return jobs.Schedule{
		Interval:   interval,
		MaxBackoff: maxBackoff,
		LeaseTTL:   batchLimit*perTaskTimeout + leaseHeadroom,
	}
}

// Run sweeps the rater queue by shelling `endless rater run`.
//
// Idempotency — the lease contract's hard requirement — comes from the Python
// side: it re-selects only tasks still missing a rating and writes only an
// axis the re-read row still has unset, so a re-claimed sweep rates each task
// at most once.
//
// A sweep that rated nothing because the model failed still exits zero: the
// rater is fail-open, and records each such task as a WARN-0029 fault itself.
// A non-zero exit here means the CLI itself broke, which is a job failure.
//
// stdout and stderr are CAPTURED, never inherited: jobs.Job forbids writing to
// either, because the trigger may be a live TUI painting the same terminal.
// They are folded into the returned error so a failure is still legible in the
// recorded fault, and the last line of a success becomes the run's note.
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
		err = fmt.Errorf("rater sweep: %w", err)
		goto end
	}
	cmd = exec.CommandContext(ctx, bin, append(dbArgs, runArgs()...)...)
	cmd.Dir = dir

	out, err = cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("rater sweep: %w: %s", err, tail(string(out)))
		goto end
	}
	jobs.Note(ctx, summary(string(out)))

end:
	return err
}

// runArgs is the `endless` argv for one sweep: every project, capped. No
// session flag is needed: the Python side emits as actor.kind `triager`, which
// carries no session and so never consults session resolution.
func runArgs() []string {
	return []string{
		"rater", "run",
		"--all-projects",
		"--limit", strconv.Itoa(batchLimit),
	}
}

// summary is the run's note: the sweep's last output line, which the Python
// side writes as a one-line tally.
func summary(out string) (note string) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// tailLimit bounds how much subprocess output rides along in a fault. The
// recorded detail is for a human looking at a broken rater, not an archive.
const tailLimit = 2000

func tail(s string) (out string) {
	out = strings.TrimSpace(s)
	if len(out) > tailLimit {
		out = "…" + out[len(out)-tailLimit:]
	}
	return out
}
