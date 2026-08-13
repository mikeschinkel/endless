// Package triagejob is the background sweep behind the description-sufficiency
// triager (E-1859), and the fire-once runner's first real client.
//
// # Belt and suspenders
//
// Triage runs twice over. `endless task add` spawns the triage of that ONE task
// detached, so an interactive filing is routed within seconds without the
// filing waiting on a model call — that is the belt, and it is a latency
// optimization only. This job is the suspenders: a periodic sweep over the
// whole `untriaged` queue that catches every task the inline path missed,
// because the child died, because `claude` was unreachable, or because the task
// was filed by something that never had an inline path at all.
//
// The job therefore assumes nothing about the inline path working. Its
// correctness argument does not depend on it.
//
// # Why this is a subprocess and not Go
//
// The decision logic lives in `src/endless/triage.py`, because the model call
// goes through `run_internal_claude`, which is Python and stays Python under
// E-1486's boundary. A Go reimplementation here would be a SECOND
// model-invocation harness in a second language — precisely what E-1849 would
// then have to unify. So this job is thin: it shells the Python CLI and reports
// what happened.
package triagejob

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// JobName keys this job's scheduling row. It must stay stable: changing it
// orphans the existing row and restarts the job's history from zero.
const JobName = "triage-sufficiency"

const (
	// batchLimit caps one sweep. Every selected task becomes a model call, so
	// this is a spend cap as much as a batch size. It mirrors
	// triage.DEFAULT_BATCH_LIMIT; the flag is passed explicitly rather than
	// left to the Python default so the lease arithmetic below is derived from
	// a number this file can see.
	batchLimit = 10

	// perTaskTimeout mirrors triage.CALL_TIMEOUT_SECONDS. Used only to derive
	// the lease; the Python side enforces it.
	perTaskTimeout = 120 * time.Second

	// interval is the sweep cadence.
	//
	// It does NOT trade against model spend, contrary to what an earlier
	// version of this comment claimed: the sweep selects `untriaged` rows
	// first, so an empty queue costs one query. Spend is proportional to tasks
	// FILED, not to how often we poll.
	//
	// Five minutes, not one: with many `session monitor` instances live, each
	// firing RunDue, tighter polling buys latency that the per-task claim
	// (monitor.ClaimTriage) should be providing instead.
	interval = 5 * time.Minute

	// maxBackoff caps exponential backoff after consecutive failures.
	// internal/jobs names model-calling jobs as exactly the case MaxBackoff
	// exists for: a persistently broken triager decays toward this cap instead
	// of burning model spend every interval, and stays visible in `jobs list`
	// rather than going silently dark.
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

// Run sweeps the untriaged queue by shelling `endless triage run`.
//
// Idempotency — the lease contract's hard requirement — is free here: the
// Python side re-selects only still-`untriaged` rows and guards each write on
// the row still being `untriaged`, so a re-claimed sweep transitions each task
// at most once.
//
// stdout and stderr are CAPTURED, never inherited: jobs.Job forbids writing to
// either, because the trigger may be a live TUI painting the same terminal.
// They are folded into the returned error so a failure is still legible in the
// recorded fault.
func (job) Run(ctx context.Context) (err error) {
	var cmd *exec.Cmd
	var out []byte
	var bin string

	bin, err = exec.LookPath("endless")
	if err != nil {
		err = fmt.Errorf("%w: the Python CLI is not on PATH", err)
		goto end
	}

	cmd = exec.CommandContext(ctx, bin,
		"triage", "run",
		"--all-projects",
		"--limit", strconv.Itoa(batchLimit),
	)
	cmd.Env = childEnv()
	// A neutral working directory, deliberately. The Python CLI refuses to
	// touch a database from inside a self-dev worktree without an explicit
	// --db (E-1429), and the runner's cwd is whatever the trigger happened to
	// be run from. Routing by environment from a directory that belongs to no
	// project makes the child's DB resolution depend only on what this
	// function passes it.
	cmd.Dir = os.TempDir()

	out, err = cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("triage sweep: %w: %s", err, tail(string(out)))
		goto end
	}

end:
	return err
}

// childEnv is the parent environment with XDG_CONFIG_HOME repointed at the
// runner's RESOLVED config directory, so the subprocess opens the database this
// process is using rather than whatever the ambient environment names.
//
// The Python CLI has no --config-dir — that flag is Go-side only — so the
// environment is the whole mechanism. Endless's config dir is always
// <XDG_CONFIG_HOME>/endless, so handing the child the PARENT of ConfigDir()
// reproduces the resolution exactly, sandbox included. Without this, a self-dev
// sweep would triage the wrong ledger.
func childEnv() (env []string) {
	const key = "XDG_CONFIG_HOME"
	value := filepath.Dir(monitor.ConfigDir())

	env = make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, key+"=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, key+"="+value)
}

// tailLimit bounds how much subprocess output rides along in a fault. The
// recorded detail is for a human triaging a broken triager, not an archive.
const tailLimit = 2000

func tail(s string) (out string) {
	out = strings.TrimSpace(s)
	if len(out) > tailLimit {
		out = "…" + out[len(out)-tailLimit:]
	}
	return out
}
