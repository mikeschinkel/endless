package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/mikeschinkel/go-doterr"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// SQL time expressions. Every timestamp this package reads or writes MUST use
// this exact format: the rest of schema.sql stores `%Y-%m-%dT%H:%M:%S`, and
// SQLite's own datetime() emits a SPACE separator instead of the 'T'. Mixing the
// two silently breaks every comparison, because ' ' sorts before 'T' — a row
// written with datetime() would look permanently overdue against one written
// with strftime().
const (
	sqlNow       = `strftime('%Y-%m-%dT%H:%M:%S', 'now')`
	sqlNowOffset = `strftime('%Y-%m-%dT%H:%M:%S', 'now', ?)`
)

// Outcome is what happened to one job during a single RunDue invocation.
type Outcome struct {
	Name    string
	Claimed bool          // false when another invocation won the race, or it was not due
	Err     error         // non-nil when the job ran and failed
	Elapsed time.Duration // wall time of Run; zero when not claimed
}

// Result is the summary of one fire-once invocation.
type Result struct {
	Outcomes []Outcome
}

// Claimed returns how many jobs this invocation actually ran.
func (r Result) Claimed() (n int) {
	for _, o := range r.Outcomes {
		if o.Claimed {
			n++
		}
	}
	return n
}

// Failed returns how many claimed jobs returned an error.
func (r Result) Failed() (n int) {
	for _, o := range r.Outcomes {
		if o.Err != nil {
			n++
		}
	}
	return n
}

// RunDue executes every registered job that is currently due, then returns.
// This is the whole runner: one pass, no loop, no timer.
//
// It NEVER returns an error and never panics. Failures are recorded as faults
// and reflected in the Result, because the caller may be a live TUI's render
// tick where an error return would have nowhere to go but the screen.
//
// Jobs run sequentially. Parallelism across a single invocation would buy
// nothing — concurrency here is BETWEEN invocations, and that is what the lease
// arbitrates.
func RunDue(ctx context.Context) (result Result) {
	var db *sql.DB
	var err error
	var job Job

	db, err = monitor.DB()
	if err != nil {
		faults.Record(faults.Fault{
			Code:        faults.ErrCodeJobScheduling,
			Source:      "jobs",
			Fingerprint: "runner-db-unavailable",
			Summary:     "background job runner could not open the database",
			Detail:      err.Error(),
		})
		goto end
	}

	for _, job = range Registered() {
		result.Outcomes = append(result.Outcomes, runOne(ctx, db, job))
	}

end:
	return result
}

// RunNamed forces one named job to run NOW, bypassing the due check but NOT the
// lease — a job already running elsewhere is still skipped. Backs
// `endless jobs run --job <name>`.
func RunNamed(ctx context.Context, name string) (outcome Outcome, err error) {
	var db *sql.DB
	var job Job
	var ok bool

	job, ok = Lookup(name)
	if !ok {
		err = doterr.NewErr(ErrJobs, ErrUnknownJob, "name", name)
		goto end
	}

	db, err = monitor.DB()
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, err)
		goto end
	}

	err = ensureRow(db, job.Name())
	if err != nil {
		goto end
	}
	err = markDue(db, job.Name())
	if err != nil {
		goto end
	}
	outcome = runOne(ctx, db, job)

end:
	return outcome, err
}

// runOne claims, runs and releases a single job. Every failure path records a
// fault and returns a populated Outcome rather than propagating an error.
func runOne(ctx context.Context, db *sql.DB, job Job) (outcome Outcome) {
	var schedule Schedule
	var owner string
	var claimed bool
	var failCount int
	var err error
	var runCtx context.Context
	var cancel context.CancelFunc
	var started time.Time
	var captured *bytes.Buffer
	var restoreLog func()

	outcome.Name = job.Name()
	schedule = job.Schedule()

	if schedule.Interval <= 0 {
		outcome.Err = doterr.NewErr(ErrJobs, ErrNoInterval, "job", job.Name())
		recordSchedulingFault(job.Name(), "job declares a non-positive interval", outcome.Err)
		goto end
	}

	err = ensureRow(db, job.Name())
	if err != nil {
		outcome.Err = err
		recordSchedulingFault(job.Name(), "job scheduling row could not be created", err)
		goto end
	}

	owner = leaseOwner()
	claimed, failCount, err = claim(db, job.Name(), owner, schedule.leaseTTL())
	if err != nil {
		outcome.Err = err
		recordSchedulingFault(job.Name(), "job could not be claimed", err)
		goto end
	}
	if !claimed {
		goto end
	}
	outcome.Claimed = true

	// The lease TTL doubles as the job's deadline: a job may not outlive the
	// claim that protects it from concurrent execution.
	runCtx, cancel = context.WithTimeout(ctx, schedule.leaseTTL())
	captured, restoreLog = captureLog()
	started = time.Now()

	outcome.Err = runGuarded(runCtx, job)

	outcome.Elapsed = time.Since(started)
	restoreLog()
	cancel()

	complete(db, job, owner, failCount, outcome.Err, captured.String())

end:
	return outcome
}

// runGuarded runs a job behind a recover(). An unrecovered panic in a job would
// kill the whole trigger process — which, for the session monitor, means the
// user's live dashboard vanishing back to a shell prompt. The panic value and
// stack become error metadata and reach the detail log.
func runGuarded(ctx context.Context, job Job) (err error) {
	defer func() {
		recovered := recover()
		if recovered != nil {
			err = doterr.NewErr(ErrJobs, ErrJobPanicked,
				"job", job.Name(),
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
		}
	}()

	err = job.Run(ctx)
	return err
}

// ensureRow creates the job's scheduling row if absent, due immediately. A newly
// registered job therefore runs on the next tick rather than waiting out an
// interval it was never scheduled for.
func ensureRow(db *sql.DB, name string) (err error) {
	_, err = db.Exec(
		`INSERT INTO jobs (name, next_due_at, created_at, updated_at)
		 VALUES (?, `+sqlNow+`, `+sqlNow+`, `+sqlNow+`)
		 ON CONFLICT (name) DO NOTHING`,
		name,
	)
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, ErrQuery, "job", name, err)
	}
	return err
}

// markDue forces a job due now without touching its failure history. Used by
// RunNamed; `jobs retry` uses Retry, which also clears the backoff.
func markDue(db *sql.DB, name string) (err error) {
	_, err = db.Exec(
		`UPDATE jobs SET next_due_at = `+sqlNow+`, updated_at = `+sqlNow+` WHERE name = ?`,
		name,
	)
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, ErrQuery, "job", name, err)
	}
	return err
}

// claim attempts the compare-and-set that grants exclusive ownership of a due
// job. This single statement IS the concurrency control: its WHERE clause
// asserts "due, and either unleased or the lease has expired", so of N racing
// processes exactly one gets RowsAffected == 1.
//
// It returns the job's CURRENT consecutive-failure count alongside, so complete
// can compute the backoff without a second read.
func claim(db *sql.DB, name, owner string, ttl time.Duration) (claimed bool, failCount int, err error) {
	err = db.QueryRow(
		`UPDATE jobs
		    SET lease_owner      = ?,
		        lease_expires_at = `+sqlNowOffset+`,
		        updated_at       = `+sqlNow+`
		  WHERE name = ?
		    AND next_due_at <= `+sqlNow+`
		    AND (lease_owner IS NULL OR lease_expires_at <= `+sqlNow+`)
		  RETURNING fail_count`,
		owner, secondsOffset(ttl), name,
	).Scan(&failCount)

	if errors.Is(err, sql.ErrNoRows) {
		// Not due, or another invocation won the race. Both are ordinary.
		err = nil
		goto end
	}
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrClaiming, ErrQuery, "job", name, err)
		goto end
	}
	claimed = true

end:
	return claimed, failCount, err
}

// complete releases the lease and reschedules, recording the run's outcome.
//
// The `lease_owner = ?` predicate is what detects a STOLEN lease: if the job
// outran its TTL and another invocation re-claimed it, this update matches zero
// rows. That is worth surfacing — it means the job's LeaseTTL is mistuned and
// two invocations may have run it concurrently.
func complete(db *sql.DB, job Job, owner string, priorFailCount int, runErr error, capturedLog string) {
	var schedule Schedule
	var failCount int
	var lastError any
	var result sql.Result
	var affected int64
	var err error

	schedule = job.Schedule()
	if runErr != nil {
		failCount = priorFailCount + 1
		lastError = runErr.Error()
	}

	result, err = db.Exec(
		`UPDATE jobs
		    SET lease_owner      = NULL,
		        lease_expires_at = NULL,
		        last_run_at      = `+sqlNow+`,
		        last_ok_at       = CASE WHEN ? THEN `+sqlNow+` ELSE last_ok_at END,
		        last_error       = ?,
		        run_count        = run_count + 1,
		        fail_count       = ?,
		        next_due_at      = `+sqlNowOffset+`,
		        updated_at       = `+sqlNow+`
		  WHERE name = ? AND lease_owner = ?`,
		runErr == nil, lastError, failCount,
		secondsOffset(schedule.nextDelay(failCount)), job.Name(), owner,
	)
	if err != nil {
		recordSchedulingFault(job.Name(), "job result could not be recorded", err)
		goto end
	}

	affected, err = result.RowsAffected()
	if err != nil {
		recordSchedulingFault(job.Name(), "job result could not be recorded", err)
		goto end
	}
	if affected == 0 {
		faults.Record(faults.Fault{
			Code:        faults.ErrCodeJobStuckLease,
			Source:      "job:" + job.Name(),
			Fingerprint: "stuck-lease:" + job.Name(),
			Summary:     `job "` + job.Name() + `" outran its lease and was re-claimed mid-run`,
			Detail: "The lease was owned by another invocation when this one finished, " +
				"so two invocations may have run concurrently. Raise the job's " +
				"Schedule.LeaseTTL above its realistic worst-case runtime.",
			Fields: map[string]any{
				"job":      job.Name(),
				"owner":    owner,
				"leaseTTL": schedule.leaseTTL().String(),
				"elapsed":  nil,
			},
		})
	}

end:
	if runErr != nil {
		recordRunFault(job, runErr, capturedLog, failCount, schedule.nextDelay(failCount))
	}
}

// recordRunFault classifies a failed run and records it. The captured log output
// rides along in the detail so a job's own diagnostics are not lost to the
// logger redirection.
func recordRunFault(job Job, runErr error, capturedLog string, failCount int, nextIn time.Duration) {
	var code faults.Code
	var detail string

	code = faults.ErrCodeJobFailed
	switch {
	case errors.Is(runErr, ErrJobPanicked):
		code = faults.ErrCodeJobPanicked
	case errors.Is(runErr, context.DeadlineExceeded):
		code = faults.ErrCodeJobTimedOut
	}

	detail = runErr.Error()
	if capturedLog != "" {
		detail += "\n\n--- job log output ---\n" + capturedLog
	}

	faults.Record(faults.Fault{
		Code:        code,
		Source:      "job:" + job.Name(),
		Fingerprint: code.Slug + ":" + job.Name(),
		Summary:     `job "` + job.Name() + `" failed: ` + firstLine(runErr.Error()),
		Detail:      detail,
		Fields: map[string]any{
			"job":                 job.Name(),
			"consecutiveFailures": failCount,
			"nextAttemptIn":       nextIn.String(),
		},
	})
}

// recordSchedulingFault reports a failure of the runner's own bookkeeping, as
// opposed to a failure of the job itself.
func recordSchedulingFault(name, summary string, err error) {
	faults.Record(faults.Fault{
		Code:        faults.ErrCodeJobScheduling,
		Source:      "job:" + name,
		Fingerprint: "scheduling:" + name,
		Summary:     summary + `: "` + name + `"`,
		Detail:      err.Error(),
		Fields:      map[string]any{"job": name},
	})
}

// firstLine trims an error message to its first line for the short summary
// field, which must stay one readable line in `errors show` and the badge.
func firstLine(s string) (out string) {
	out = s
	for i, r := range s {
		if r == '\n' {
			out = s[:i]
			break
		}
	}
	if len(out) > 160 {
		out = out[:157] + "..."
	}
	return out
}

// secondsOffset renders a duration as a SQLite datetime modifier.
func secondsOffset(d time.Duration) (offset string) {
	seconds := int64(d / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	offset = "+" + strconv.FormatInt(seconds, 10) + " seconds"
	return offset
}

var (
	ownerOnce  sync.Once
	ownerValue string
)

// leaseOwner returns this process's opaque lease token. Host and pid identify a
// live process; the start-time nonce keeps a recycled pid from being mistaken
// for the original owner.
func leaseOwner() (owner string) {
	ownerOnce.Do(func() {
		host, err := os.Hostname()
		if err != nil {
			host = "unknown"
		}
		ownerValue = host + ":" +
			strconv.Itoa(os.Getpid()) + ":" +
			strconv.FormatInt(time.Now().UnixNano(), 36)
	})
	return ownerValue
}

// logMu serializes logger redirection. Jobs run sequentially within an
// invocation, but the trigger may fire RunDue from a goroutine, so the swap and
// its restore must not interleave with another.
var logMu sync.Mutex

// captureLog redirects the standard logger into a buffer and returns it with a
// restore func.
//
// This exists because log.Printf writes to STDERR while the session monitor
// paints STDOUT with cursor-home escape sequences. An unredirected log line from
// a job lands on top of the drawn table, and because monitorLoop only repaints
// when the frame content changes, the corruption persists on an idle dashboard
// rather than being cleaned up on the next tick.
//
// Captured output is not discarded: a failing job's log rides into its fault
// detail, so redirecting costs no diagnostic information. (The 42 PRE-EXISTING
// log.Printf call sites elsewhere in the codebase are converted separately —
// E-1884; this only contains the seam E-698 creates.)
func captureLog() (buffer *bytes.Buffer, restore func()) {
	logMu.Lock()
	prevWriter := log.Writer()
	prevFlags := log.Flags()

	buffer = &bytes.Buffer{}
	log.SetOutput(buffer)

	return buffer, func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		logMu.Unlock()
	}
}
