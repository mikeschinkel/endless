package sessionstatuscmd

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faultrow"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// bindFaultStore points the faults package at a throwaway in-memory DB for one
// test, and unbinds afterwards so tests that expect NO fault row still see none.
//
// A near-copy of the helper in internal/faultrow, and deliberately so: these
// tests assert the fault row reaches the SESSION-STATUS FRAME, which is a
// property of renderTo, not of the row. Exporting a test helper across a package
// boundary to save fifteen lines would couple two suites that must be able to
// fail independently.
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
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	// A registered project, so a test can file a fault under one and have the
	// foreign key on errors.project_id accept it.
	if _, err = db.Exec(
		`INSERT INTO projects (id, name, path) VALUES (1, 'elsewhere', '~/Projects/elsewhere')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
		nil,
	)
	t.Cleanup(func() { faults.Bind(nil, nil, nil) })
}

// oneRow is the minimal row set that exercises the normal (non-empty) render
// path, so the fault row is asserted where it will actually be seen.
func oneRow() []monitor.SessionStatusRow {
	return []monitor.SessionStatusRow{
		{ID: 698, Title: "Build a fire-once background job runner", Status: "underway",
			Phase: "now", TypeSlug: "todo", IsFocal: true},
	}
}

func TestRenderFaultRow_AppearsWhenIncidentsAreOpen(t *testing.T) {
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
		t.Errorf("the fault row does not show the ERROR severity:\n%s", out)
	}
	if !strings.Contains(out, "1 error") {
		t.Errorf("the fault row does not count the error:\n%s", out)
	}
	if !strings.Contains(out, "1 warning") {
		t.Errorf("the fault row does not count the warning:\n%s", out)
	}
	if !strings.Contains(out, faultrow.Hint) {
		t.Errorf("the fault row does not name the command that explains it:\n%s", out)
	}

	// The task rows must still be intact — the fault row annotates the view, it does
	// not replace it.
	if !strings.Contains(out, "E-698") {
		t.Errorf("task row is missing from the frame:\n%s", out)
	}
}

// TestRenderFaultRow_StaysMachineWide is the session-status half of E-1960's
// scoping contract, and the deliberate asymmetry with `project status`.
//
// `session status` renders every live session on the box, whatever project each
// is in, so its fault row counts every project's open incidents. Narrowing it to the
// project the pane happens to sit in would hide a fault in a project this very
// frame is showing a session for. `project status` makes the opposite call — see
// internal/projectstatuscmd/faultrow_scope_test.go — because that view is scoped to
// one project in every other respect.
func TestRenderFaultRow_StaysMachineWide(t *testing.T) {
	bindFaultStore(t)

	// One fault filed under a project this session has nothing to do with. It is
	// the only ERROR, so the severity chip is the tell.
	faults.Record(faults.Fault{
		Code:      faults.ErrCodeJobPanicked,
		ProjectID: 1,
		Source:    "job:elsewhere",
		Summary:   `a job in another project panicked`,
	})

	var b strings.Builder
	renderTo(&b, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)
	out := b.String()

	if !strings.Contains(out, "ERROR") {
		t.Errorf("session status narrowed its fault row to one project — "+
			"a fault in another project vanished from a machine-wide view:\n%s", out)
	}
}

func TestRenderFaultRow_SilentWhenNothingIsOpen(t *testing.T) {
	bindFaultStore(t)

	var b strings.Builder
	renderTo(&b, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)

	if strings.Contains(b.String(), faultrow.Hint) {
		t.Errorf("the fault row rendered with no open incidents:\n%s", b.String())
	}
}

func TestRenderFaultRow_SilentWhenClearedEvenThoughHistoryRemains(t *testing.T) {
	bindFaultStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:fixed",
		Summary: `job "fixed" failed`,
	})
	if _, err := faults.Clear(faults.AllProjects, nil, "tester"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	var b strings.Builder
	renderTo(&b, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)

	if strings.Contains(b.String(), faultrow.Hint) {
		t.Errorf("the fault row still rendered after clearing:\n%s", b.String())
	}
}

func TestRenderFaultRow_AppearsOnTheEmptyView(t *testing.T) {
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

	if !strings.Contains(b.String(), faultrow.Hint) {
		t.Errorf("the fault row is missing from the empty view:\n%s", b.String())
	}
}

func TestRenderFaultRow_ColorizesOnlyWhenColorIsEnabled(t *testing.T) {
	bindFaultStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobPanicked,
		Source:  "job:exploding",
		Summary: `job "exploding" panicked`,
	})

	var plain strings.Builder
	renderTo(&plain, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)
	if strings.Contains(plain.String(), "\033[") {
		t.Errorf("the fault row emitted ANSI escapes with color disabled:\n%q", plain.String())
	}

	var colored strings.Builder
	renderTo(&colored, oneRow(), 698, hintClaimBind, 90, true, hiddenOmit)
	// Isolate the fault row's own line before asserting: with color on the legend
	// is dimmed too, so a frame-wide escape check would pass without the row being
	// styled at all. WHICH colors it uses is pinned by
	// TestRowLine_UsesThemeIndependentColors, next to the constants.
	row := faultRowLineOf(colored.String())
	if row == "" {
		t.Fatalf("no fault row in the colored frame:\n%q", colored.String())
	}
	if !strings.Contains(row, "\033[") {
		t.Errorf("the fault row carries no ANSI escapes with color enabled:\n%q", row)
	}
}

// faultRowLineOf returns the frame line carrying the fault row, or "".
func faultRowLineOf(frame string) string {
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, faultrow.Hint) {
			return line
		}
	}
	return ""
}

func TestRenderFaultRow_SurvivesAnUnboundFaultStore(t *testing.T) {
	faults.Bind(nil, nil, nil)

	// A diagnostics surface must never be able to take down the view it
	// annotates: with no fault store reachable the frame renders as normal,
	// simply without a fault row.
	var b strings.Builder
	renderTo(&b, oneRow(), 698, hintClaimBind, 90, false, hiddenOmit)

	if !strings.Contains(b.String(), "E-698") {
		t.Errorf("frame did not render with an unbound fault store:\n%s", b.String())
	}
	if strings.Contains(b.String(), faultrow.Hint) {
		t.Errorf("the fault row rendered with an unbound fault store:\n%s", b.String())
	}
}
