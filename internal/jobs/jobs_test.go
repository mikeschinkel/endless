package jobs

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// fakeJob is a Job whose behavior each test declares inline. Run counts its own
// invocations so a test can assert what actually executed rather than inferring
// it from the scheduling row.
type fakeJob struct {
	name     string
	schedule Schedule
	runs     atomic.Int32
	fn       func(ctx context.Context) error
}

func (f *fakeJob) Name() string       { return f.name }
func (f *fakeJob) Schedule() Schedule { return f.schedule }

func (f *fakeJob) Run(ctx context.Context) error {
	f.runs.Add(1)
	if f.fn == nil {
		return nil
	}
	return f.fn(ctx)
}

// newTestDB builds an in-memory DB carrying the real schema, binds it as the
// process DB, and wires the fault recorder at it. One connection, matching
// monitor.DB()'s production setting, so SQLite's single-writer semantics are the
// same ones the runner sees in the field.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("close db: %v", cerr)
		}
	})

	if _, err = db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	t.Cleanup(monitor.SetTestDB(db))
	faults.Bind(monitor.DB, func() string {
		return filepath.Join(monitor.ConfigDir(), "log")
	})
	return db
}

// register adds a job for the duration of one test.
func register(t *testing.T, job Job) {
	t.Helper()
	t.Cleanup(resetRegistryForTest())
	Register(job)
}

func TestRunDue_RunsARegisteredJobAndReschedulesIt(t *testing.T) {
	db := newTestDB(t)
	job := &fakeJob{name: "hourly", schedule: Schedule{Interval: time.Hour}}
	register(t, job)

	result := RunDue(context.Background())

	if got := job.runs.Load(); got != 1 {
		t.Fatalf("job ran %d times, want 1", got)
	}
	if result.Claimed() != 1 {
		t.Errorf("Claimed() = %d, want 1", result.Claimed())
	}
	if result.Failed() != 0 {
		t.Errorf("Failed() = %d, want 0", result.Failed())
	}

	// Side effects on the scheduling row, not just the return value.
	var runCount, failCount int
	var owner sql.NullString
	err := db.QueryRow(
		`SELECT run_count, fail_count, lease_owner FROM jobs WHERE name = 'hourly'`,
	).Scan(&runCount, &failCount, &owner)
	if err != nil {
		t.Fatalf("read jobs row: %v", err)
	}
	if runCount != 1 {
		t.Errorf("run_count = %d, want 1", runCount)
	}
	if failCount != 0 {
		t.Errorf("fail_count = %d, want 0", failCount)
	}
	if owner.Valid {
		t.Errorf("lease_owner = %q, want NULL after completion", owner.String)
	}

	// A second immediate invocation must not re-run it: the reschedule pushed
	// next_due_at an hour out.
	RunDue(context.Background())
	if got := job.runs.Load(); got != 1 {
		t.Errorf("job ran %d times after a second RunDue, want 1", got)
	}
}

func TestRunDue_ConcurrentInvocationsRunTheJobExactlyOnce(t *testing.T) {
	newTestDB(t)

	// A job slow enough that every racer is inside RunDue at the same moment.
	job := &fakeJob{
		name:     "contended",
		schedule: Schedule{Interval: time.Hour},
		fn: func(context.Context) error {
			time.Sleep(20 * time.Millisecond)
			return nil
		},
	}
	register(t, job)

	const racers = 8
	var claimed atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed.Add(int32(RunDue(context.Background()).Claimed()))
		}()
	}
	close(start)
	wg.Wait()

	// This is the whole point of the compare-and-set lease: N monitors may fire
	// the runner simultaneously, and exactly one of them runs a given due job.
	if got := claimed.Load(); got != 1 {
		t.Errorf("%d invocations claimed the job, want exactly 1", got)
	}
	if got := job.runs.Load(); got != 1 {
		t.Errorf("job ran %d times, want exactly 1", got)
	}
}

func TestRunDue_ExpiredLeaseIsReclaimable(t *testing.T) {
	db := newTestDB(t)
	job := &fakeJob{name: "abandoned", schedule: Schedule{Interval: time.Hour}}
	register(t, job)

	// Simulate a process that claimed the job and died mid-run: the row is due,
	// leased to someone else, and the lease has already expired. Nothing cleans
	// this up — the next invocation simply re-claims it.
	_, err := db.Exec(
		`INSERT INTO jobs (name, next_due_at, lease_owner, lease_expires_at)
		 VALUES ('abandoned',
		         strftime('%Y-%m-%dT%H:%M:%S', 'now', '-1 hour'),
		         'dead-process',
		         strftime('%Y-%m-%dT%H:%M:%S', 'now', '-30 minutes'))`,
	)
	if err != nil {
		t.Fatalf("seed abandoned lease: %v", err)
	}

	RunDue(context.Background())

	if got := job.runs.Load(); got != 1 {
		t.Errorf("job ran %d times, want 1 (an expired lease must be reclaimable)", got)
	}
}

func TestRunDue_LiveLeaseBlocksAnotherInvocation(t *testing.T) {
	db := newTestDB(t)
	job := &fakeJob{name: "held", schedule: Schedule{Interval: time.Hour}}
	register(t, job)

	// Due, but a live lease is outstanding and has NOT expired.
	_, err := db.Exec(
		`INSERT INTO jobs (name, next_due_at, lease_owner, lease_expires_at)
		 VALUES ('held',
		         strftime('%Y-%m-%dT%H:%M:%S', 'now', '-1 hour'),
		         'other-process',
		         strftime('%Y-%m-%dT%H:%M:%S', 'now', '+1 hour'))`,
	)
	if err != nil {
		t.Fatalf("seed live lease: %v", err)
	}

	RunDue(context.Background())

	if got := job.runs.Load(); got != 0 {
		t.Errorf("job ran %d times, want 0 while another invocation holds the lease", got)
	}
}

func TestRunDue_FailureRecordsAFaultAndCountsTheFailure(t *testing.T) {
	db := newTestDB(t)
	register(t, &fakeJob{
		name:     "broken",
		schedule: Schedule{Interval: time.Minute},
		fn: func(context.Context) error {
			return errors.New("upstream unavailable")
		},
	})

	result := RunDue(context.Background())

	if result.Failed() != 1 {
		t.Fatalf("Failed() = %d, want 1", result.Failed())
	}

	var failCount int
	var lastError sql.NullString
	err := db.QueryRow(
		`SELECT fail_count, last_error FROM jobs WHERE name = 'broken'`,
	).Scan(&failCount, &lastError)
	if err != nil {
		t.Fatalf("read jobs row: %v", err)
	}
	if failCount != 1 {
		t.Errorf("fail_count = %d, want 1", failCount)
	}
	if lastError.String == "" {
		t.Error("last_error is empty, want the job's error message")
	}

	incidents, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list faults: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("recorded %d faults, want 1", len(incidents))
	}
	if incidents[0].Code != faults.ErrCodeJobFailed.ID {
		t.Errorf("fault code = %s, want %s", incidents[0].Code, faults.ErrCodeJobFailed.ID)
	}
	if incidents[0].Source != "job:broken" {
		t.Errorf("fault source = %s, want job:broken", incidents[0].Source)
	}
}

func TestRunDue_PanicIsRecoveredAndRecorded(t *testing.T) {
	db := newTestDB(t)
	register(t, &fakeJob{
		name:     "exploding",
		schedule: Schedule{Interval: time.Minute},
		fn: func(context.Context) error {
			panic("boom")
		},
	})

	// The assertion is partly that this call RETURNS at all: an unrecovered
	// panic here would take down the session monitor in production.
	result := RunDue(context.Background())

	if result.Failed() != 1 {
		t.Fatalf("Failed() = %d, want 1", result.Failed())
	}
	if !errors.Is(result.Outcomes[0].Err, ErrJobPanicked) {
		t.Errorf("outcome error = %v, want it to wrap ErrJobPanicked", result.Outcomes[0].Err)
	}

	// The lease must still have been released, or the job would be stuck.
	var owner sql.NullString
	if err := db.QueryRow(`SELECT lease_owner FROM jobs WHERE name = 'exploding'`).Scan(&owner); err != nil {
		t.Fatalf("read jobs row: %v", err)
	}
	if owner.Valid {
		t.Errorf("lease_owner = %q, want NULL after a panicking run", owner.String)
	}

	incidents, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list faults: %v", err)
	}
	if len(incidents) != 1 || incidents[0].Code != faults.ErrCodeJobPanicked.ID {
		t.Fatalf("want one %s fault, got %+v", faults.ErrCodeJobPanicked.ID, incidents)
	}
	if incidents[0].Severity != faults.SeverityError {
		t.Errorf("panic severity = %s, want %s", incidents[0].Severity, faults.SeverityError)
	}
}

func TestRunDue_StdoutLoggingIsCapturedIntoTheFaultDetail(t *testing.T) {
	newTestDB(t)
	register(t, &fakeJob{
		name:     "chatty",
		schedule: Schedule{Interval: time.Minute},
		fn: func(context.Context) error {
			log.Print("diagnostic from inside the job")
			return errors.New("failed after logging")
		},
	})

	RunDue(context.Background())

	incidents, err := faults.List(false, 0)
	if err != nil {
		t.Fatalf("list faults: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("recorded %d faults, want 1", len(incidents))
	}

	details, err := faults.Details(incidents[0].ID)
	if err != nil {
		t.Fatalf("read detail log: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("logged %d occurrences, want 1", len(details))
	}
	// Redirecting the logger must not COST diagnostics: what the job logged has
	// to reach the detail record instead of the terminal.
	if !strings.Contains(details[0].Detail, "diagnostic from inside the job") {
		t.Errorf("detail does not carry the job's log output:\n%s", details[0].Detail)
	}
}

func TestRunNamed_ForcesAJobThatIsNotYetDue(t *testing.T) {
	newTestDB(t)
	job := &fakeJob{name: "forced", schedule: Schedule{Interval: 24 * time.Hour}}
	register(t, job)

	RunDue(context.Background()) // first run schedules it a day out
	if got := job.runs.Load(); got != 1 {
		t.Fatalf("job ran %d times after RunDue, want 1", got)
	}

	if _, err := RunNamed(context.Background(), "forced"); err != nil {
		t.Fatalf("RunNamed: %v", err)
	}
	if got := job.runs.Load(); got != 2 {
		t.Errorf("job ran %d times after RunNamed, want 2", got)
	}
}

func TestRetry_ClearsBackoffAndMakesTheJobDue(t *testing.T) {
	db := newTestDB(t)
	register(t, &fakeJob{
		name:     "backed-off",
		schedule: Schedule{Interval: time.Minute, MaxBackoff: time.Hour},
		fn: func(context.Context) error {
			return errors.New("still broken")
		},
	})

	RunDue(context.Background())

	if err := Retry("backed-off"); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	var failCount int
	var lastError sql.NullString
	var overdue bool
	err := db.QueryRow(
		`SELECT fail_count, last_error, next_due_at <= strftime('%Y-%m-%dT%H:%M:%S','now')
		   FROM jobs WHERE name = 'backed-off'`,
	).Scan(&failCount, &lastError, &overdue)
	if err != nil {
		t.Fatalf("read jobs row: %v", err)
	}
	if failCount != 0 {
		t.Errorf("fail_count = %d, want 0 after retry", failCount)
	}
	if lastError.Valid {
		t.Errorf("last_error = %q, want NULL after retry", lastError.String)
	}
	if !overdue {
		t.Error("next_due_at is in the future, want the job due immediately after retry")
	}
}

func TestRetry_UnknownJobIsAnError(t *testing.T) {
	newTestDB(t)
	t.Cleanup(resetRegistryForTest())

	err := Retry("no-such-job")
	if !errors.Is(err, ErrUnknownJob) {
		t.Errorf("Retry(unknown) = %v, want ErrUnknownJob", err)
	}
}

func TestSchedule_NextDelayBacksOffExponentiallyAndCaps(t *testing.T) {
	withBackoff := Schedule{Interval: time.Minute, MaxBackoff: 10 * time.Minute}

	// failCount is the count AFTER the run: 0 means success.
	assertDelay(t, "success resets to the interval", withBackoff.nextDelay(0), time.Minute)
	assertDelay(t, "first failure", withBackoff.nextDelay(1), time.Minute)
	assertDelay(t, "second failure doubles", withBackoff.nextDelay(2), 2*time.Minute)
	assertDelay(t, "third failure doubles again", withBackoff.nextDelay(3), 4*time.Minute)
	assertDelay(t, "fourth failure doubles again", withBackoff.nextDelay(4), 8*time.Minute)
	assertDelay(t, "fifth failure hits the cap", withBackoff.nextDelay(5), 10*time.Minute)
	assertDelay(t, "a long-dead job stays at the cap", withBackoff.nextDelay(500), 10*time.Minute)

	// Without MaxBackoff there is no backoff at all: a cheap idempotent job
	// simply retries at its normal cadence.
	noBackoff := Schedule{Interval: 30 * time.Second}
	assertDelay(t, "no backoff configured, first failure", noBackoff.nextDelay(1), 30*time.Second)
	assertDelay(t, "no backoff configured, many failures", noBackoff.nextDelay(99), 30*time.Second)
}

func assertDelay(t *testing.T, what string, got, want time.Duration) {
	t.Helper()
	if got != want {
		t.Errorf("%s: delay = %s, want %s", what, got, want)
	}
}

func TestSchedule_LeaseTTLDefaultsToTwiceTheIntervalWithAFloor(t *testing.T) {
	if got := (Schedule{Interval: time.Hour}).leaseTTL(); got != 2*time.Hour {
		t.Errorf("leaseTTL for a 1h interval = %s, want 2h", got)
	}
	// A short interval must not produce a lease too brief to finish under load.
	if got := (Schedule{Interval: time.Second}).leaseTTL(); got != 5*time.Minute {
		t.Errorf("leaseTTL for a 1s interval = %s, want the 5m floor", got)
	}
	if got := (Schedule{Interval: time.Hour, LeaseTTL: time.Minute}).leaseTTL(); got != time.Minute {
		t.Errorf("explicit leaseTTL = %s, want it to win", got)
	}
}

func TestRegistered_IsEmptyByDefault(t *testing.T) {
	t.Cleanup(resetRegistryForTest())

	// E-698 ships the runner with NO production jobs: the runner knows nothing
	// job-specific, and E-1859 / E-1881 are its first clients. If this ever
	// fails, a job was registered without the deliberate decision to ship one.
	if got := len(Registered()); got != 0 {
		t.Errorf("Registered() has %d jobs before any test registers one, want 0", got)
	}
}

func TestRegister_RejectsADuplicateName(t *testing.T) {
	t.Cleanup(resetRegistryForTest())
	Register(&fakeJob{name: "dupe", schedule: Schedule{Interval: time.Minute}})

	defer func() {
		if recover() == nil {
			t.Error("registering a duplicate name did not panic")
		}
	}()
	Register(&fakeJob{name: "dupe", schedule: Schedule{Interval: time.Minute}})
}
