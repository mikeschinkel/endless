package faultbadge

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
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

// --- E-1950: one-line badge, stale-warning age-off, non-redundant counts ---

// activeClock returns an activeSince function that reports a fixed amount of
// active time regardless of the timestamp asked about.
func activeClock(d time.Duration) func(time.Time) (time.Duration, error) {
	return func(time.Time) (time.Duration, error) { return d, nil }
}

// brokenClock stands in for an unreadable activity table.
func brokenClock() func(time.Time) (time.Duration, error) {
	return func(time.Time) (time.Duration, error) {
		return 0, errors.New("activity table unreadable")
	}
}

func warned(lastSeen string) faults.Incident {
	return faults.Incident{
		ID: 1, Code: "ERR-0004", Severity: faults.SeverityWarning,
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

func TestBadgeworthy_DropsAWarningOnlyAfterAnActiveHour(t *testing.T) {
	stale := warned("2026-08-10T09:49:09")

	kept := badgeworthy([]faults.Incident{stale}, activeClock(59*time.Minute))
	if len(kept) != 1 {
		t.Errorf("warning dropped before an active hour elapsed: kept=%d, want 1", len(kept))
	}

	kept = badgeworthy([]faults.Incident{stale}, activeClock(time.Hour))
	if len(kept) != 0 {
		t.Errorf("warning survived a full active hour: kept=%d, want 0", len(kept))
	}
}

func TestBadgeworthy_NeverAgesOutAnError(t *testing.T) {
	// An error is the case the manual-clear rule was written for: it stays until
	// someone acknowledges it, however long ago it last fired.
	kept := badgeworthy([]faults.Incident{errored("2020-01-01T00:00:00")}, activeClock(1000*time.Hour))
	if len(kept) != 1 {
		t.Errorf("error aged off the badge: kept=%d, want 1", len(kept))
	}
}

func TestBadgeworthy_KeepsWhatItCannotMeasure(t *testing.T) {
	// Unreadable activity table and unparseable timestamp both mean "cannot
	// justify hiding this", which must never resolve to hiding it.
	kept := badgeworthy([]faults.Incident{warned("2026-08-10T09:49:09")}, brokenClock())
	if len(kept) != 1 {
		t.Errorf("warning hidden despite an unreadable clock: kept=%d, want 1", len(kept))
	}

	kept = badgeworthy([]faults.Incident{warned("not-a-timestamp")}, activeClock(1000*time.Hour))
	if len(kept) != 1 {
		t.Errorf("warning hidden on an unparseable timestamp: kept=%d, want 1", len(kept))
	}
}

func TestBadgeLine_IsOneRowCarryingBothTextAndHint(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := badgeLine(overview, 90, false)

	if strings.Contains(line, "\n") {
		t.Errorf("badge spans more than one row:\n%q", line)
	}
	if !strings.Contains(line, "WARNING") {
		t.Errorf("badge lost its severity chip:\n%q", line)
	}
	if !strings.Contains(line, "ERR-0004") {
		t.Errorf("badge lost the incident code:\n%q", line)
	}
	if !strings.HasSuffix(line, Hint) {
		t.Errorf("hint is not right-aligned at the end of the row:\n%q", line)
	}
}

func TestBadgeLine_OmitsTheCountASingleChipAlreadyConveys(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := badgeLine(overview, 90, false)

	// " WARNING  1 warning — ..." said the same thing twice.
	if strings.Contains(line, "1 warning") {
		t.Errorf("badge restates the count the chip already carries:\n%q", line)
	}
}

func TestBadgeLine_CountsWhenThereIsMoreThanOneIncident(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{
		errored("2026-08-10T10:00:00"),
		warned("2026-08-10T09:49:09"),
	})

	line := badgeLine(overview, 90, false)

	if !strings.Contains(line, "1 error") || !strings.Contains(line, "1 warning") {
		t.Errorf("badge dropped the tally that the single chip cannot convey:\n%q", line)
	}
}

func TestBadgeLine_KeepsTheTextWhenTheRowIsTooNarrowForBoth(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := badgeLine(overview, 30, false)

	if strings.Contains(line, "\n") {
		t.Errorf("narrow badge wrapped onto a second row:\n%q", line)
	}
	if !strings.Contains(line, "ERR-0004") {
		t.Errorf("narrow badge dropped the incident text instead of the hint:\n%q", line)
	}
}

func TestBadgeLine_UsesThemeIndependentColors(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	line := badgeLine(overview, 90, true)

	// The 30-47 ANSI range is remapped by the terminal theme, which is what made
	// the old black-on-yellow chip unreadable. 256-color indices are fixed.
	if strings.Contains(line, "\033[30;43m") {
		t.Errorf("badge fell back to theme-remapped ANSI colors:\n%q", line)
	}
	if !strings.Contains(line, rowWarning) {
		t.Errorf("badge did not reverse the whole row:\n%q", line)
	}
	if !strings.Contains(line, chipWarning) {
		t.Errorf("chip is not inverted against the row:\n%q", line)
	}
}

// --- E-1950: the badge must be correct at EVERY width, not at a chosen one ---
//
// Terminal width is not a property of any one person's setup: it changes with
// the monitor, the split, the font, and the window. Asserting the layout at a
// hand-picked width only moves the guess around, so these sweep the range and
// assert the invariants that must hold at all of them.

// badgeWidths is the sweep: absurdly narrow through wider than any real
// terminal, including every boundary the layout logic can turn on.
func badgeWidths() (widths []int) {
	for w := 1; w <= 240; w++ {
		widths = append(widths, w)
	}
	return widths
}

// printedWidth is the badge's width in terminal columns, with the ANSI escapes
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

func TestBadgeLine_NeverExceedsTheTerminalWidth(t *testing.T) {
	cases := map[string]faults.Overview{
		"warning":    faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")}),
		"error":      faults.Summarize([]faults.Incident{errored("2026-08-10T10:00:00")}),
		"both":       faults.Summarize([]faults.Incident{errored("2026-08-10T10:00:00"), warned("2026-08-10T09:49:09")}),
		"long":       faults.Summarize([]faults.Incident{longWarning()}),
		"wide runes": faults.Summarize([]faults.Incident{wideRuneWarning()}),
	}

	for name, overview := range cases {
		for _, cols := range badgeWidths() {
			for _, color := range []bool{false, true} {
				line := badgeLine(overview, cols, color)

				if strings.Contains(line, "\n") {
					t.Fatalf("%s/cols=%d/color=%v: badge contains a newline:\n%q", name, cols, color, line)
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

func TestBadgeLine_AlwaysShowsTheSeverityAndTheCode(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	// Whatever else gives way, the severity is the last thing standing: a badge
	// that cannot say what happened is not worth the row it costs.
	//
	// Both thresholds are DERIVED from the layout's own constants, not chosen.
	// A hand-picked width would just be a different guess about someone's
	// terminal, and would silently stop testing the boundary the moment the
	// chip text or the hint changed length.
	chip := severityLabel(faults.SeverityWarning)
	chipWidth := runewidth.StringWidth(chip)
	// The bare severity word must fit whole; below that the badge renders nothing.
	minSeverity := runewidth.StringWidth(strings.TrimSpace(chip))
	minCode := chipWidth + 2 + len("ERR-0004…") // chip, space, and a code that survives truncation

	for _, cols := range badgeWidths() {
		line := badgeLine(overview, cols, false)

		if cols-1 < minSeverity {
			if line != "" {
				t.Fatalf("cols=%d: rendered a badge too narrow to be legible:\n%q", cols, line)
			}
			continue
		}
		if !strings.Contains(line, "WARNING") {
			t.Fatalf("cols=%d: badge lost its severity:\n%q", cols, line)
		}
		if cols >= minCode && !strings.Contains(line, "ERR-0004") {
			t.Fatalf("cols=%d: badge lost the incident code:\n%q", cols, line)
		}
	}
}

func TestBadgeLine_DropsTheHintOnlyWhenItCannotFit(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{longWarning()})

	// The hint is reserved BEFORE the text, so at any width where both fit it is
	// present — and once present it must never disappear again as the terminal
	// gets wider. Assert that transition happens exactly once.
	seen := false
	for _, cols := range badgeWidths() {
		line := badgeLine(overview, cols, false)
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

func TestBadgeLine_ResetsEveryColorItOpens(t *testing.T) {
	overview := faults.Summarize([]faults.Incident{warned("2026-08-10T09:49:09")})

	// An unreset background bleeds into the rest of the pane — including the
	// user's prompt after the command exits.
	for _, cols := range badgeWidths() {
		line := badgeLine(overview, cols, true)
		if line == "" {
			continue // too narrow to render at all — nothing opened, nothing to reset
		}
		if !strings.HasSuffix(line, badgeReset) {
			t.Fatalf("cols=%d: badge does not end with a reset:\n%q", cols, line)
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
