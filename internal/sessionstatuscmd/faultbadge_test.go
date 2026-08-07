package sessionstatuscmd

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// bindFaultStore points the faults package at a throwaway in-memory DB for one
// test, and unbinds afterwards so tests that expect NO badge still see none.
func bindFaultStore(t *testing.T) {
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

	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
	)
	t.Cleanup(func() { faults.Bind(nil, nil) })
}

// oneRow is the minimal row set that exercises the normal (non-empty) render
// path, so the badge is asserted where it will actually be seen.
func oneRow() []monitor.SessionStatusRow {
	return []monitor.SessionStatusRow{
		{ID: 698, Title: "Build a fire-once background job runner", Status: "underway",
			Phase: "now", TypeSlug: "todo", IsFocal: true},
	}
}

func TestRenderFaultBadge_AppearsWhenIncidentsAreOpen(t *testing.T) {
	bindFaultStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobPanicked,
		Source:  "job:exploding",
		Summary: `job "exploding" panicked`,
	})
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:flaky",
		Summary: `job "flaky" failed`,
	})

	var b strings.Builder
	renderTo(&b, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)
	out := b.String()

	// Max severity wins: one error outranks any number of warnings.
	if !strings.Contains(out, "ERROR") {
		t.Errorf("badge does not show the ERROR severity:\n%s", out)
	}
	if !strings.Contains(out, "1 error") {
		t.Errorf("badge does not count the error:\n%s", out)
	}
	if !strings.Contains(out, "1 warning") {
		t.Errorf("badge does not count the warning:\n%s", out)
	}
	if !strings.Contains(out, "endless errors show") {
		t.Errorf("badge does not name the command that explains it:\n%s", out)
	}

	// The task rows must still be intact — the badge annotates the view, it does
	// not replace it.
	if !strings.Contains(out, "E-698") {
		t.Errorf("task row is missing from the frame:\n%s", out)
	}
}

func TestRenderFaultBadge_SilentWhenNothingIsOpen(t *testing.T) {
	bindFaultStore(t)

	var b strings.Builder
	renderTo(&b, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)

	if strings.Contains(b.String(), "endless errors show") {
		t.Errorf("badge rendered with no open incidents:\n%s", b.String())
	}
}

func TestRenderFaultBadge_SilentWhenClearedEvenThoughHistoryRemains(t *testing.T) {
	bindFaultStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:fixed",
		Summary: `job "fixed" failed`,
	})
	if _, err := faults.Clear(nil, "tester"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	var b strings.Builder
	renderTo(&b, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)

	if strings.Contains(b.String(), "endless errors show") {
		t.Errorf("badge still rendered after clearing:\n%s", b.String())
	}
}

func TestRenderFaultBadge_AppearsOnTheEmptyView(t *testing.T) {
	bindFaultStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:flaky",
		Summary: `job "flaky" failed`,
	})

	// A session with no rows at all still has to surface faults; otherwise the
	// state in which a user is MOST likely to be idle is the one that hides them.
	var b strings.Builder
	renderTo(&b, nil, 0, hintClaimBind, 90, false, hiddenOmit)

	if !strings.Contains(b.String(), "endless errors show") {
		t.Errorf("badge missing from the empty view:\n%s", b.String())
	}
}

func TestRenderFaultBadge_ColorizesOnlyWhenColorIsEnabled(t *testing.T) {
	bindFaultStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobPanicked,
		Source:  "job:exploding",
		Summary: `job "exploding" panicked`,
	})

	var plain strings.Builder
	renderTo(&plain, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)
	if strings.Contains(plain.String(), "\033[") {
		t.Errorf("badge emitted ANSI escapes with color disabled:\n%q", plain.String())
	}

	var colored strings.Builder
	renderTo(&colored, oneRow(), 698, hintClaimBind, 90, true, hiddenOmit)
	if !strings.Contains(colored.String(), badgeError) {
		t.Errorf("badge did not use the error background with color enabled:\n%q", colored.String())
	}
}

func TestRenderFaultBadge_SurvivesAnUnboundFaultStore(t *testing.T) {
	faults.Bind(nil, nil)

	// A diagnostics surface must never be able to take down the view it
	// annotates: with no fault store reachable the frame renders as normal,
	// simply without a badge.
	var b strings.Builder
	renderTo(&b, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)

	if !strings.Contains(b.String(), "E-698") {
		t.Errorf("frame did not render with an unbound fault store:\n%s", b.String())
	}
	if strings.Contains(b.String(), "endless errors show") {
		t.Errorf("badge rendered with an unbound fault store:\n%s", b.String())
	}
}
