package faultrow

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/schema"
)

// bindFaultStore points the faults package at a throwaway in-memory DB for one
// test, and unbinds afterwards so tests that expect NO fault row still see none.
func bindFaultStore(t *testing.T) *sql.DB {
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

	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return db, nil },
		func() string { return logDir },
		nil,
	)
	t.Cleanup(func() { faults.Bind(nil, nil, nil) })

	return db
}

// ancient is a last-occurrence far enough back that any age-off, keyed to any
// clock, would have fired long ago.
const ancient = "2020-01-01T00:00:00"

// age backdates every open incident's last occurrence, so a test can ask what
// the fault row does with a fault nobody has seen fire in years.
func age(t *testing.T, db *sql.DB, lastSeen string) {
	t.Helper()

	if _, err := db.Exec(`UPDATE errors SET last_seen_at = ?`, lastSeen); err != nil {
		t.Fatalf("backdate incidents: %v", err)
	}
}

// rendered is the fault row Render writes for the bound store, at a width wide
// enough that nothing is truncated away.
func rendered(t *testing.T) string {
	t.Helper()

	var out strings.Builder
	Render(&out, 120, false, faults.AllProjects)
	return out.String()
}

// --- E-2151: nothing ages off the row; clearing is the only way out ---
//
// A warning used to leave the row an hour of active time after it last fired,
// which made a fault that happened once and self-healed discoverable only by
// someone who already suspected it existed. The listed set is now exactly the
// uncleared set, at both severities, whatever their age.

// recordWarning opens a warning-severity incident in the bound store.
func recordWarning() {
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobScheduling,
		Source:  "job:triage",
		Summary: "job scheduling row could not be created",
	})
}

// recordError opens an error-severity incident in the bound store.
func recordError() {
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobPanicked,
		Source:  "job:exploding",
		Summary: `job "exploding" panicked`,
	})
}

func TestRender_ShowsAWarningHoweverLongAgoItFired(t *testing.T) {
	db := bindFaultStore(t)

	recordWarning()
	age(t, db, ancient)

	line := rendered(t)
	if !strings.Contains(line, "WARNING") {
		t.Errorf("an old warning aged off the fault row:\n%q", line)
	}
	if !strings.Contains(line, "WARN-0004") {
		t.Errorf("the fault row lost the incident code:\n%q", line)
	}
}

func TestRender_CountsEveryUnclearedIncidentHoweverOld(t *testing.T) {
	db := bindFaultStore(t)

	recordWarning()
	recordError()
	age(t, db, ancient)

	// The tally is what the single chip cannot convey, so it is where a row
	// counting a filtered set rather than the open one shows up: an aged-off
	// warning would leave the ERROR chip standing over no tally at all.
	line := rendered(t)
	if !strings.Contains(line, "ERROR") {
		t.Errorf("an old error aged off the fault row:\n%q", line)
	}
	if !strings.Contains(line, "1 error") || !strings.Contains(line, "1 warning") {
		t.Errorf("the fault row counts fewer incidents than the store holds open:\n%q", line)
	}
}

func TestRender_DropsOnlyWhatSomebodyCleared(t *testing.T) {
	db := bindFaultStore(t)

	recordWarning()
	age(t, db, ancient)

	// Clearing is the one thing that takes an incident off the row — which is
	// what makes row noise the user's to manage rather than a timer's.
	if _, err := faults.Clear(faults.AllProjects, nil, "test"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if line := rendered(t); line != "" {
		t.Errorf("the fault row survived the clear:\n%q", line)
	}
}

// --- E-1950: one-line fault row, non-redundant counts ---

func warned(lastSeen string) faults.Incident {
	return faults.Incident{
		ID: 1, Code: "WARN-0004", Severity: faults.SeverityWarning,
		Source: "job:triage", Summary: "job scheduling row could not be created",
		Occurrences: 1, LastSeenAt: lastSeen,
	}
}

func errored(lastSeen string) faults.Incident {
	return faults.Incident{
		ID: 2, Code: "ERR-0002", Severity: faults.SeverityError,
		Source: "job:exploding", Summary: `job "exploding" panicked`,
		Occurrences: 1, LastSeenAt: lastSeen,
	}
}

func TestRowLine_IsOneRowCarryingBothTextAndHint(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := rowLine(overview, 90, false)

	if strings.Contains(line, "\n") {
		t.Errorf("the fault row spans more than one line:\n%q", line)
	}
	if !strings.Contains(line, "WARNING") {
		t.Errorf("the fault row lost its severity chip:\n%q", line)
	}
	if !strings.Contains(line, "WARN-0004") {
		t.Errorf("the fault row lost the incident code:\n%q", line)
	}
	if !strings.HasSuffix(line, Hint) {
		t.Errorf("hint is not right-aligned at the end of the row:\n%q", line)
	}
}

func TestRowLine_OmitsTheCountASingleChipAlreadyConveys(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := rowLine(overview, 90, false)

	// " WARNING  1 warning — ..." said the same thing twice.
	if strings.Contains(line, "1 warning") {
		t.Errorf("the fault row restates the count the chip already carries:\n%q", line)
	}
}

func TestRowLine_CountsWhenThereIsMoreThanOneIncident(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{
		errored("2026-08-10T10:00:00"),
		warned("2026-08-10T09:49:09"),
	})

	line := rowLine(overview, 90, false)

	if !strings.Contains(line, "1 error") || !strings.Contains(line, "1 warning") {
		t.Errorf("the fault row dropped the tally that the single chip cannot convey:\n%q", line)
	}
}

func TestRowLine_KeepsTheTextWhenTheRowIsTooNarrowForBoth(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := rowLine(overview, 30, false)

	if strings.Contains(line, "\n") {
		t.Errorf("a narrow fault row wrapped onto a second line:\n%q", line)
	}
	if !strings.Contains(line, "WARN-0004") {
		t.Errorf("a narrow fault row dropped the incident text instead of the hint:\n%q", line)
	}
}

func TestRowLine_UsesThemeIndependentColors(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := rowLine(overview, 90, true)

	// The 30-47 ANSI range is remapped by the terminal theme, which is what made
	// the old black-on-yellow chip unreadable. 256-color indices are fixed.
	if strings.Contains(line, "\033[30;43m") {
		t.Errorf("the fault row fell back to theme-remapped ANSI colors:\n%q", line)
	}
	if !strings.Contains(line, rowWarning) {
		t.Errorf("the fault row did not reverse its whole line:\n%q", line)
	}
	if !strings.Contains(line, chipWarning) {
		t.Errorf("chip is not inverted against the row:\n%q", line)
	}
}

// --- E-1950: the row must be correct at EVERY width, not at a chosen one ---
//
// Terminal width is not a property of any one person's setup: it changes with
// the monitor, the split, the font, and the window. Asserting the layout at a
// hand-picked width only moves the guess around, so these sweep the range and
// assert the invariants that must hold at all of them.

// rowWidths is the sweep: absurdly narrow through wider than any real
// terminal, including every boundary the layout logic can turn on.
func rowWidths() (widths []int) {
	for w := 1; w <= 240; w++ {
		widths = append(widths, w)
	}
	return widths
}

// printedWidth is the row's width in terminal columns, with the ANSI escapes
// — which occupy no columns — removed.
func printedWidth(line string) int {
	var out strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] != '\033' {
			out.WriteByte(line[i])
			continue
		}
		for i < len(line) && line[i] != 'm' {
			i++
		}
	}
	return runewidth.StringWidth(out.String())
}

func TestRowLine_NeverExceedsTheTerminalWidth(t *testing.T) {
	cases := map[string]faults.Overview{
		"warning":    faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")}),
		"error":      faults.Summarize([]faults.Incident{errored("2026-08-10T10:00:00")}),
		"both":       faults.Summarize([]faults.Incident{errored("2026-08-10T10:00:00"), warned("2026-08-10T09:49:09")}),
		"long":       faults.Summarize([]faults.Incident{longWarning()}),
		"wide runes": faults.Summarize([]faults.Incident{wideRuneWarning()}),
	}

	for name, overview := range cases {
		for _, cols := range rowWidths() {
			for _, color := range []bool{false, true} {
				line := rowLine(overview, cols, color)

				if strings.Contains(line, "\n") {
					t.Fatalf("%s/cols=%d/color=%v: the fault row contains a newline:\n%q", name, cols, color, line)
				}
				// cols-1, not cols: a line ending exactly at the right margin sits
				// on the deferred-wrap boundary where tmux can emit a phantom row.
				if got := printedWidth(line); got > cols-1 {
					t.Fatalf("%s/cols=%d/color=%v: printed width %d exceeds cols-1:\n%q",
						name, cols, color, got, line)
				}
			}
		}
	}
}

func TestRowLine_AlwaysShowsTheSeverityAndTheCode(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	// Whatever else gives way, the severity is the last thing standing: a row
	// that cannot say what happened is not worth the row it costs.
	//
	// Both thresholds are DERIVED from the layout's own constants, not chosen.
	// A hand-picked width would just be a different guess about someone's
	// terminal, and would silently stop testing the boundary the moment the
	// chip text or the hint changed length.
	chip := severityLabel(faults.SeverityWarning)
	chipWidth := runewidth.StringWidth(chip)
	// The bare severity word must fit whole; below that the row renders nothing.
	minSeverity := runewidth.StringWidth(strings.TrimSpace(chip))
	minCode := chipWidth + 2 + len("WARN-0004…") // chip, space, and a code that survives truncation

	for _, cols := range rowWidths() {
		line := rowLine(overview, cols, false)

		if cols-1 < minSeverity {
			if line != "" {
				t.Fatalf("cols=%d: rendered a fault row too narrow to be legible:\n%q", cols, line)
			}
			continue
		}
		if !strings.Contains(line, "WARNING") {
			t.Fatalf("cols=%d: the fault row lost its severity:\n%q", cols, line)
		}
		if cols >= minCode && !strings.Contains(line, "WARN-0004") {
			t.Fatalf("cols=%d: the fault row lost the incident code:\n%q", cols, line)
		}
	}
}

func TestRowLine_DropsTheHintOnlyWhenItCannotFit(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{longWarning()})

	// The hint is reserved BEFORE the text, so at any width where both fit it is
	// present — and once present it must never disappear again as the terminal
	// gets wider. Assert that transition happens exactly once.
	seen := false
	for _, cols := range rowWidths() {
		line := rowLine(overview, cols, false)
		has := strings.Contains(line, Hint)
		if has {
			seen = true
			if !strings.HasSuffix(line, Hint) {
				t.Fatalf("cols=%d: hint is present but not right-aligned:\n%q", cols, line)
			}
			continue
		}
		if seen {
			t.Fatalf("cols=%d: hint reappeared as missing after fitting at a narrower width:\n%q", cols, line)
		}
	}
	if !seen {
		t.Fatal("the hint never fit at any width up to 240")
	}
}

func TestRowLine_ResetsEveryColorItOpens(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	// An unreset background bleeds into the rest of the pane — including the
	// user's prompt after the command exits.
	for _, cols := range rowWidths() {
		line := rowLine(overview, cols, true)
		if line == "" {
			continue // too narrow to render at all — nothing opened, nothing to reset
		}
		if !strings.HasSuffix(line, rowReset) {
			t.Fatalf("cols=%d: the fault row does not end with a reset:\n%q", cols, line)
		}
	}
}

func longWarning() faults.Incident {
	w := warned("2026-08-10T09:49:09")
	w.Summary = strings.Repeat("a very long summary that will certainly not fit ", 8)
	return w
}

func wideRuneWarning() faults.Incident {
	w := warned("2026-08-10T09:49:09")
	// Double-width runes are where a byte-length budget silently overflows.
	w.Summary = strings.Repeat("日本語テキスト", 12)
	return w
}
