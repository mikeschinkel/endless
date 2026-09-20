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

	// WARN-0004 is BOTH the assertion that the warning is still counted and the
	// assertion that the row says WHICH warning — E-2148 folded those two facts
	// into one string when the severity word left the row and the code took it
	// over.
	line := rendered(t)
	if !strings.Contains(line, "WARN-0004") {
		t.Errorf("an old warning aged off the fault row:\n%q", line)
	}
}

func TestRender_CountsEveryUnclearedIncidentHoweverOld(t *testing.T) {
	db := bindFaultStore(t)

	recordWarning()
	recordError()
	age(t, db, ancient)

	// The tally is where a row counting a filtered set rather than the open one
	// shows up: an aged-off warning would leave the error counted alone.
	line := rendered(t)
	if !strings.Contains(line, glyphError+"1") {
		t.Errorf("an old error aged off the fault row:\n%q", line)
	}
	if !strings.Contains(line, glyphWarning+"1") {
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
	if !strings.Contains(line, "WARN-0004") {
		t.Errorf("the fault row lost the incident code:\n%q", line)
	}
	if !strings.Contains(line, "job scheduling row") {
		t.Errorf("a lone incident lost its summary:\n%q", line)
	}
	if !strings.HasSuffix(line, Hint) {
		t.Errorf("hint is not right-aligned at the end of the row:\n%q", line)
	}
}

// --- E-2148: the code states the severity, so the row stops spelling it out ---

func TestRowLine_SpendsNoColumnsOnTheSeverityWord(t *testing.T) {
	cases := map[string]faults.Overview{
		"warning": faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")}),
		"error":   faults.Summarize([]faults.Incident{errored("2026-08-10T10:00:00")}),
		"both": faults.Summarize([]faults.Incident{
			errored("2026-08-10T10:00:00"), warned("2026-08-10T09:49:09"),
		}),
	}

	// " WARNING " and " ERROR   " cost nine columns to repeat what WARN-0004
	// and ERR-0002 already say — taken from the summary, which is the part that
	// was being cut off.
	for name, overview := range cases {
		for _, cols := range rowWidths() {
			line := rowLine(overview, cols, false)
			for _, word := range []string{"WARNING", "ERROR"} {
				if strings.Contains(line, word) {
					t.Fatalf("%s/cols=%d: the row still spells out %q:\n%q",
						name, cols, word, line)
				}
			}
		}
	}
}

func TestRowLine_StillColorsByMaxSeverity(t *testing.T) {
	// The severity WORD went; the distinction did not. The whole row is still
	// reversed in the max severity's pair, which is what makes it read at a
	// glance from across a pane.
	warn := rowLine(faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")}), 90, true)
	if !strings.Contains(warn, rowWarning) {
		t.Errorf("a warning-only row is not painted in the warning pair:\n%q", warn)
	}

	both := rowLine(faults.Summarize([]faults.Incident{
		errored("2026-08-10T10:00:00"), warned("2026-08-10T09:49:09"),
	}), 90, true)
	if !strings.Contains(both, rowError) {
		t.Errorf("an error present did not win the row's color:\n%q", both)
	}
	if strings.Contains(both, rowWarning) {
		t.Errorf("the row carries two severity pairs at once:\n%q", both)
	}
}

// --- E-2148 second pass: the chip keeps its slot, the code takes its place ---
//
// The first pass dropped the chip along with the severity word it held. That
// threw away the row's fixed left-hand anchor to solve a problem the WORD alone
// had — and left the code as plain text on the bar, indistinguishable from the
// summary beside it.

func TestRowLine_PutsTheCodeInTheChip(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := rowLine(overview, 90, true)

	// The chip is the inverted pair. Finding the code inside it is the whole
	// assertion: on the bar it would carry the ROW's pair instead.
	chip := chipStyle(faults.SeverityWarning) + " WARN-0004 " + rowReset
	if !strings.Contains(line, chip) {
		t.Errorf("the code is not rendered as an inverted chip:\n%q\nwant to contain:\n%q",
			line, chip)
	}
	// And the summary is NOT in it.
	if strings.Contains(chip, "job scheduling") {
		t.Errorf("the chip swallowed the summary:\n%q", line)
	}
}

func TestRowLine_PutsTheTallyInTheChipWhenSeveralAreOpen(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{
		errored("2026-08-10T10:00:00"),
		warned("2026-08-10T09:49:09"),
	})

	line := rowLine(overview, 90, true)

	// With several open no single code describes the situation, so the chip
	// holds the most compressed statement that is still true.
	chip := chipStyle(faults.SeverityError) + " " + glyphError + "1 " + glyphWarning + "1 " + rowReset
	if !strings.Contains(line, chip) {
		t.Errorf("the tally is not rendered as an inverted chip:\n%q\nwant to contain:\n%q",
			line, chip)
	}
}

func TestRowLine_NeverTruncatesTheChip(t *testing.T) {
	shapes := map[string][]string{
		"one incident": {"WARN-0004"},
		"several":      {glyphError + "1", glyphWarning + "1"},
	}
	overviews := map[string]faults.Overview{
		"one incident": faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")}),
		"several": faults.Summarize([]faults.Incident{
			errored("2026-08-10T10:00:00"), warned("2026-08-10T09:49:09"),
		}),
	}

	// A code is an identifier — looked up, typed into `errors show`, pasted into
	// a bug report — and a tally cut in half misreports how much is wrong. So
	// the chip is whole or the row does not render.
	for name, overview := range overviews {
		for _, cols := range rowWidths() {
			line := rowLine(overview, cols, false)
			if line == "" {
				continue
			}
			for _, want := range shapes[name] {
				if !strings.Contains(line, want) {
					t.Fatalf("%s/cols=%d: the chip lost %q:\n%q", name, cols, want, line)
				}
			}
		}
	}
}

func TestRowLine_KeepsTheChipWhenNothingElseFits(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	// At exactly the bare code's width the chip stands alone, unpadded. Below
	// it, nothing — a sliver of an identifier is not an identifier.
	bare := "WARN-0004"
	if got := rowLine(overview, runewidth.StringWidth(bare)+1, false); got != bare {
		t.Errorf("at the bare chip's width the row is %q, want %q", got, bare)
	}
	if got := rowLine(overview, runewidth.StringWidth(bare), false); got != "" {
		t.Errorf("below the bare chip's width the row is %q, want empty", got)
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
}

func TestRowLine_TallyGlyphsAreSingleWidth(t *testing.T) {
	// Load-bearing, not cosmetic: this row is budgeted in terminal columns, so a
	// double-width glyph counted as one pushes the right-aligned hint past the
	// margin and wraps the line. Swapping in a prettier glyph must fail here
	// rather than ship.
	for _, glyph := range []string{glyphError, glyphWarning} {
		if got := runewidth.StringWidth(glyph); got != 1 {
			t.Errorf("glyph %q is %d columns wide, want 1", glyph, got)
		}
	}
}

func TestRowLine_TalliesSeveritiesInGlyphsWhenSeveralAreOpen(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{
		errored("2026-08-10T10:00:00"),
		errored("2026-08-10T09:59:00"),
		warned("2026-08-10T09:49:09"),
	})

	line := rowLine(overview, 90, false)

	if !strings.Contains(line, glyphError+"2") {
		t.Errorf("the tally does not count the two errors:\n%q", line)
	}
	if !strings.Contains(line, glyphWarning+"1") {
		t.Errorf("the tally does not count the warning:\n%q", line)
	}
	// The words are what the glyphs replaced. "1 error, 1 warning" cost
	// eighteen columns to say what "✕1 ⚠1" says in five.
	if strings.Contains(line, "error") || strings.Contains(line, "warning") {
		t.Errorf("the tally still spells the severities out:\n%q", line)
	}
}

func TestRowLine_NamesTheDistinctCodesRatherThanOneSummary(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{
		errored("2026-08-10T10:00:00"),
		warned("2026-08-10T09:49:09"),
	})

	line := rowLine(overview, 90, false)

	if !strings.Contains(line, "ERR-0002") || !strings.Contains(line, "WARN-0004") {
		t.Errorf("the row does not name both open codes:\n%q", line)
	}
	// With several open, naming ONE summary reads as the whole story when it is
	// a fraction of it.
	if strings.Contains(line, "job scheduling row") || strings.Contains(line, "panicked") {
		t.Errorf("the row named one incident's summary while several are open:\n%q", line)
	}
	// Most severe first.
	if strings.Index(line, "ERR-0002") > strings.Index(line, "WARN-0004") {
		t.Errorf("codes are not ordered most severe first:\n%q", line)
	}
}

func TestRowLine_CollapsesDuplicateCodes(t *testing.T) {
	a := warned("2026-08-10T09:49:09")
	b := warned("2026-08-10T09:48:00")
	b.ID, b.Fingerprint, b.Summary = 9, "other", "a different failure, same code"

	line := rowLine(faults.Summarize([]faults.Incident{a, b}), 90, false)

	if n := strings.Count(line, "WARN-0004"); n != 1 {
		t.Errorf("one code shown %d times; the tally already carries the count:\n%q", n, line)
	}
	if !strings.Contains(line, glyphWarning+"2") {
		t.Errorf("the tally lost an incident to the code collapse:\n%q", line)
	}
}

// wholeCodes is the set the shedding sweep matches against.
var wholeCodes = map[string]bool{"ERR-0002": true, "ERR-0011": true, "WARN-0004": true}

func TestRowLine_ShedsCodesIntoACountRatherThanCuttingOne(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{
		errored("2026-08-10T10:00:00"),
		unresolvedBranch("2026-08-10T09:55:00"),
		warned("2026-08-10T09:49:09"),
	})

	// A code is an identifier: it gets looked up, typed into `errors show` and
	// pasted into a bug report, and "WARN-00…" serves none of those. So the row
	// sheds WHOLE codes into a count as it narrows, and never cuts one in half.
	sawMore := false
	for _, cols := range rowWidths() {
		line := rowLine(overview, cols, false)
		if line == "" {
			continue
		}
		if strings.Contains(line, "more") {
			sawMore = true
		}
		// Every code-shaped TOKEN on the line must be a whole code. Tokens
		// rather than substrings is the point: "ERR-" is a prefix of two
		// different codes here, so a substring test cannot tell a shed code
		// from a cut one.
		for _, field := range strings.Fields(line) {
			if !strings.HasPrefix(field, "ERR-") && !strings.HasPrefix(field, "WARN-") {
				continue
			}
			if !wholeCodes[field] {
				t.Fatalf("cols=%d: row carries a partial code %q:\n%q", cols, field, line)
			}
		}
	}
	if !sawMore {
		t.Error("no width between 1 and 240 shed codes into a +N more count")
	}
}

func TestRowLine_KeepsTheTallyWhenNoCodeFits(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{
		errored("2026-08-10T10:00:00"),
		warned("2026-08-10T09:49:09"),
	})

	// The tally is the last thing to go: "an error and a warning are open" is
	// still true at any width, and a bare "+2 more" would be a longer way of
	// saying what the tally already said.
	tally := severityTally(overview)
	line := rowLine(overview, runewidth.StringWidth(tally)+1, false)

	if line != tally {
		t.Errorf("at exactly the tally's width the row is %q, want %q", line, tally)
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

func TestRowLine_ShowsTheWholeCodeOrNoRowAtAll(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	// With the severity word gone, the CODE is the thing that must survive: it
	// is what says both what happened and how severe it is, and it is what gets
	// typed into `errors show`. A truncated one serves neither purpose, so the
	// rule is whole-or-nothing.
	//
	// The threshold is DERIVED from the layout, not chosen: a hand-picked width
	// would just be a different guess about someone's terminal and would
	// silently stop testing the boundary the moment the code or hint changed
	// length.
	minCode := runewidth.StringWidth("WARN-0004")

	for _, cols := range rowWidths() {
		line := rowLine(overview, cols, false)

		if cols-1 < minCode {
			if line != "" {
				t.Fatalf("cols=%d: rendered a fault row too narrow to carry a whole code:\n%q",
					cols, line)
			}
			continue
		}
		if !strings.Contains(line, "WARN-0004") {
			t.Fatalf("cols=%d: the fault row lost the incident code:\n%q", cols, line)
		}
	}
}

func TestRowLine_DropsTheHintOnlyWhenItCannotFit(t *testing.T) {
	// The hint is reserved BEFORE the text, so at any width where both fit it is
	// present — and once present it must never disappear again as the terminal
	// gets wider. Assert that transition happens exactly once, for BOTH shapes
	// the row takes.
	//
	// The multi-incident case is the one that caught this: deciding from the
	// fitted text rather than from the budget made the hint blink back on as the
	// terminal NARROWED, because a tally short enough to leave room appeared at
	// a width where a code list had not.
	shapes := map[string]faults.Overview{
		"one incident": faults.Summarize([]faults.Incident{longWarning()}),
		"several": faults.Summarize([]faults.Incident{
			errored("2026-08-10T10:00:00"),
			unresolvedBranch("2026-08-10T09:55:00"),
			warned("2026-08-10T09:49:09"),
		}),
	}

	for name, overview := range shapes {
		seen := false
		for _, cols := range rowWidths() {
			line := rowLine(overview, cols, false)
			has := strings.Contains(line, Hint)
			if has {
				seen = true
				if !strings.HasSuffix(line, Hint) {
					t.Fatalf("%s/cols=%d: hint is present but not right-aligned:\n%q",
						name, cols, line)
				}
				continue
			}
			if seen {
				t.Fatalf("%s/cols=%d: hint reappeared as missing after fitting at a "+
					"narrower width:\n%q", name, cols, line)
			}
		}
		if !seen {
			t.Fatalf("%s: the hint never fit at any width up to 240", name)
		}
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

// unresolvedBranch is a THIRD distinct code, so the codes list has something to
// shed. Two codes can only ever become "one code +1 more", which does not
// exercise the shedding loop.
func unresolvedBranch(lastSeen string) faults.Incident {
	return faults.Incident{
		ID: 3, Code: "ERR-0011", Severity: faults.SeverityError,
		Source: "probe:unsettled", Summary: "the repository's default branch could not be resolved",
		Occurrences: 1, LastSeenAt: lastSeen,
	}
}

func wideRuneWarning() faults.Incident {
	w := warned("2026-08-10T09:49:09")
	// Double-width runes are where a byte-length budget silently overflows.
	w.Summary = strings.Repeat("日本語テキスト", 12)
	return w
}
