package sessionstatuscmd

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faultbadge"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// bindFaultStore points the faults package at a throwaway in-memory DB for one
// test, and unbinds afterwards so tests that expect NO badge still see none.
//
// A near-copy of the helper in internal/faultbadge, and deliberately so: these
// tests assert the badge reaches the SESSION-STATUS FRAME, which is a property
// of renderTo, not of the badge. Exporting a test helper across a package
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
	if !strings.Contains(out, faultbadge.Hint) {
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

	if strings.Contains(b.String(), faultbadge.Hint) {
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

	if strings.Contains(b.String(), faultbadge.Hint) {
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

	if !strings.Contains(b.String(), faultbadge.Hint) {
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
	// Isolate the badge's own line before asserting: with color on the legend is
	// dimmed too, so a frame-wide escape check would pass without the badge being
	// styled at all. WHICH colors it uses is pinned by
	// TestBadgeLine_UsesThemeIndependentColors, next to the constants.
	badge := badgeLineOf(colored.String())
	if badge == "" {
		t.Fatalf("no badge line in the colored frame:\n%q", colored.String())
	}
	if !strings.Contains(badge, "\033[") {
		t.Errorf("badge line carries no ANSI escapes with color enabled:\n%q", badge)
	}
}

// badgeLineOf returns the frame line carrying the fault badge, or "".
func badgeLineOf(frame string) string {
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, faultbadge.Hint) {
			return line
		}
	}
	return ""
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
	if strings.Contains(b.String(), faultbadge.Hint) {
		t.Errorf("badge rendered with an unbound fault store:\n%s", b.String())
	}
}
