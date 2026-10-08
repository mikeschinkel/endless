package jobs

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
)

// flakyJob fails with whatever err currently holds; nil makes it succeed.
func flakyJob(t *testing.T, name string, err *error) {
	t.Helper()
	register(t, &fakeJob{
		name:     name,
		schedule: Schedule{Interval: time.Minute, MaxBackoff: time.Hour},
		fn:       func(context.Context) error { return *err },
	})
}

// runN forces the named job to run n times, ignoring its backoff.
func runN(t *testing.T, name string, n int) (last Outcome) {
	t.Helper()
	for i := 0; i < n; i++ {
		var err error
		last, err = RunNamed(context.Background(), name)
		if err != nil {
			t.Fatalf("RunNamed %s: %v", name, err)
		}
	}
	return last
}

func openIncidents(t *testing.T) []faults.Incident {
	t.Helper()
	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list faults: %v", err)
	}
	return incidents
}

// TestTransient_BelowThresholdRecordsNothingButBacksOff: a brief outage leaves
// the error list alone, while the scheduling row still counts the failures and
// pushes the next attempt out.
func TestTransient_BelowThresholdRecordsNothingButBacksOff(t *testing.T) {
	db := newTestDB(t)
	cause := Transient(errors.New("ssh: Could not resolve hostname github.com"))
	flakyJob(t, "net", &cause)

	runN(t, "net", TransientThreshold-1)

	if got := openIncidents(t); len(got) != 0 {
		t.Fatalf("recorded %d incidents below the threshold, want 0: %+v", len(got), got)
	}
	var failCount int
	var lastError sql.NullString
	var backedOff bool
	err := db.QueryRow(
		`SELECT fail_count, last_error,
		        next_due_at > strftime('%Y-%m-%dT%H:%M:%S', 'now', '+90 seconds')
		   FROM jobs WHERE name = 'net'`,
	).Scan(&failCount, &lastError, &backedOff)
	if err != nil {
		t.Fatalf("read jobs row: %v", err)
	}
	if failCount != TransientThreshold-1 {
		t.Errorf("fail_count = %d, want %d", failCount, TransientThreshold-1)
	}
	if !strings.Contains(lastError.String, "Could not resolve hostname") {
		t.Errorf("last_error = %q, want the cause", lastError.String)
	}
	if !backedOff {
		t.Error("next_due_at is within the base interval: the backoff did not escalate")
	}
}

// TestTransient_ThresholdRecordsUnreachableNotJobFailed: the Nth failure in a
// row is WARN-0032, and WARN-0001 is never recorded for it.
func TestTransient_ThresholdRecordsUnreachableNotJobFailed(t *testing.T) {
	newTestDB(t)
	cause := Transient(errors.New("ssh: Could not resolve hostname github.com\nfatal: more"))
	flakyJob(t, "net", &cause)

	runN(t, "net", TransientThreshold)

	got := openIncidents(t)
	if len(got) != 1 {
		t.Fatalf("recorded %d incidents, want 1: %+v", len(got), got)
	}
	if got[0].Code != faults.ErrCodeJobUnreachable.ID {
		t.Errorf("code = %s, want %s", got[0].Code, faults.ErrCodeJobUnreachable.ID)
	}
	if got[0].Fingerprint != "job-unreachable:net" {
		t.Errorf("fingerprint = %q", got[0].Fingerprint)
	}
	want := `job "net" failed 4 times in a row: ssh: Could not resolve hostname github.com`
	if got[0].Summary != want {
		t.Errorf("summary = %q, want %q", got[0].Summary, want)
	}
}

// TestTransient_NonTransientReportsAtOnce: a failure the job did not mark
// transient is WARN-0001 on its first run, whatever transient failures came
// before it.
func TestTransient_NonTransientReportsAtOnce(t *testing.T) {
	newTestDB(t)
	cause := errors.New("rejected by policy")
	flakyJob(t, "push", &cause)

	runN(t, "push", 1)
	got := openIncidents(t)
	if len(got) != 1 || got[0].Code != faults.ErrCodeJobFailed.ID {
		t.Fatalf("incidents = %+v, want one %s", got, faults.ErrCodeJobFailed.ID)
	}

	newTestDB(t)
	cause = Transient(errors.New("connection refused"))
	runN(t, "push", 2)
	cause = errors.New("rejected by policy")
	runN(t, "push", 1)
	got = openIncidents(t)
	if len(got) != 1 || got[0].Code != faults.ErrCodeJobFailed.ID {
		t.Fatalf("after transient failures, incidents = %+v, want one %s", got, faults.ErrCodeJobFailed.ID)
	}
}

// TestTransient_SuccessClearsTheWarning: once the outage passes, the job's next
// successful run resolves WARN-0032 without a person.
func TestTransient_SuccessClearsTheWarning(t *testing.T) {
	newTestDB(t)
	cause := Transient(errors.New("Operation timed out"))
	flakyJob(t, "net", &cause)
	runN(t, "net", TransientThreshold)
	if len(openIncidents(t)) != 1 {
		t.Fatal("no warning at the threshold")
	}

	cause = nil
	if out := runN(t, "net", 1); out.Err != nil {
		t.Fatalf("run failed: %v", out.Err)
	}
	if got := openIncidents(t); len(got) != 0 {
		t.Errorf("warning still open after a successful run: %+v", got)
	}
}

// TestTransient_FailedClearIsNotTheRunsError: the clear is best-effort.
func TestTransient_FailedClearIsNotTheRunsError(t *testing.T) {
	newTestDB(t)
	prev := clearFingerprint
	clearFingerprint = func(string, string, string) (int, error) { return 0, errors.New("disk on fire") }
	t.Cleanup(func() { clearFingerprint = prev })
	var cause error
	flakyJob(t, "ok", &cause)

	if out := runN(t, "ok", 1); out.Err != nil {
		t.Errorf("a failed clear became the run's error: %v", out.Err)
	}
}

// TestTransient_KeepsTheCauseReachable: marking an error transient must not
// hide it from errors.Is/As, and marking nil is still nil.
func TestTransient_KeepsTheCauseReachable(t *testing.T) {
	cause := &fs.PathError{Op: "open", Path: "x", Err: fs.ErrNotExist}
	err := Transient(cause)

	if !IsTransient(err) {
		t.Error("IsTransient(Transient(err)) = false")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Error("errors.Is lost the cause")
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) || pe != cause {
		t.Error("errors.As lost the cause")
	}
	if err.Error() != cause.Error() {
		t.Errorf("message = %q, want %q", err.Error(), cause.Error())
	}
	if !IsTransient(errors.Join(errors.New("ctx"), err)) {
		t.Error("IsTransient did not see through a wrap")
	}
	if IsTransient(cause) {
		t.Error("an unmarked error reads as transient")
	}
	if Transient(nil) != nil {
		t.Error("Transient(nil) != nil")
	}
}
