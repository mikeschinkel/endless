package monitor

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/schema"
)

// E-1940 decided that a probe failure records a FAULT (E-698's system) rather
// than inventing a `?` glyph and a legend entry. That only works if the fault
// survives being raised from a render tick — `session monitor` re-probes every
// row every two seconds, so a fault raised per occurrence would bury the
// incident list under thousands of copies of one condition.

// bindFaultsForTest points the faults package at a fresh in-memory store and
// returns it. Mirrors newBoundStore in the faults package's own tests; it
// cannot be shared, because that one lives in package faults_test.
func bindFaultsForTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err = db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
		nil,
	)
	t.Cleanup(func() { faults.Bind(nil, nil, nil) })
	return db
}

// TestProbeFailureRecordsOneIncidentPerProbe is the dedup requirement stated as
// a test: fifty ticks over one broken worktree must leave ONE open incident
// with fifty occurrences, not fifty incidents.
func TestProbeFailureRecordsOneIncidentPerProbe(t *testing.T) {
	bindFaultsForTest(t)
	unsettledStub{revListErr: errors.New("fatal: bad revision")}.install(t)

	const ticks = 50
	for i := 0; i < ticks; i++ {
		if !WorktreeUnsettledAt("/wt/e-1940").Unsettled() {
			t.Fatal("a failed probe must not read as the all-clear")
		}
	}

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents from %d ticks, want 1", len(incidents), ticks)
	}
	got := incidents[0]
	if got.Code != faults.ErrCodeWorktreeProbeFailed.ID {
		t.Errorf("code = %s, want %s", got.Code, faults.ErrCodeWorktreeProbeFailed.ID)
	}
	if got.Occurrences != ticks {
		t.Errorf("occurrences = %d, want %d", got.Occurrences, ticks)
	}
	// The list is read to find out WHICH task is unverifiable; a bare path
	// buries that.
	if !strings.Contains(got.Summary, "E-1940") {
		t.Errorf("summary %q does not name the task", got.Summary)
	}
}

// TestDistinctProbesRaiseDistinctIncidents is the other side of the dedup key:
// two worktrees, or two different failing probes, are two conditions to fix and
// must not collapse into one row.
func TestDistinctProbesRaiseDistinctIncidents(t *testing.T) {
	bindFaultsForTest(t)

	unsettledStub{statusErr: errors.New("fatal: not a git repository")}.install(t)
	WorktreeUnsettledAt("/wt/e-1000")
	WorktreeUnsettledAt("/wt/e-2000")

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(incidents) != 2 {
		t.Fatalf("two worktrees produced %d incidents, want 2", len(incidents))
	}
}

// TestUnresolvedDefaultBranchRecordsItsOwnCode keeps the two failures
// distinguishable in the incident list: an unresolvable default branch disables
// every probe in the project at once and has a different remedy from one
// worktree whose git call failed.
func TestUnresolvedDefaultBranchRecordsItsOwnCode(t *testing.T) {
	bindFaultsForTest(t)
	unsettledStub{baseUnresolvable: true}.install(t)

	WorktreeUnsettledAt("/wt/e-1940")

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("got %d incidents, want 1", len(incidents))
	}
	if incidents[0].Code != faults.ErrCodeDefaultBranchUnresolved.ID {
		t.Errorf("code = %s, want %s",
			incidents[0].Code, faults.ErrCodeDefaultBranchUnresolved.ID)
	}
}

// TestSettledWorktreeRecordsNothing guards against the fault channel becoming
// noise: the normal case must be silent, or nobody will read the list where the
// real failures land.
func TestSettledWorktreeRecordsNothing(t *testing.T) {
	bindFaultsForTest(t)
	unsettledStub{status: "", revList: "0\n"}.install(t)

	if WorktreeUnsettledAt("/wt/e-1940").Unsettled() {
		t.Fatal("clean and fully landed must be settled")
	}

	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list incidents: %v", err)
	}
	if len(incidents) != 0 {
		t.Errorf("a settled worktree recorded %d incidents: %+v", len(incidents), incidents)
	}
}
