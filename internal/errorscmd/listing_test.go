package errorscmd

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faults"
)

// The listing WRAPPED (E-2148 item 4). It rendered through text/tabwriter,
// which pads to the widest cell and knows nothing about the terminal, so a long
// summary ran past the margin and the row folded — on the one surface whose job
// is to be scannable when something is wrong.
//
// Terminal width is not a property of any one person's setup: it changes with
// the monitor, the split, the font and the window. So these sweep the range
// rather than asserting the layout at a width somebody guessed.

func warned() faults.Incident {
	return faults.Incident{
		ID: 7, Code: "WARN-0004", Severity: faults.SeverityWarning,
		Project: "endless", Source: "job:triage",
		Summary:     "job scheduling row could not be created after three attempts, each behind the connection's five-second busy timeout",
		Occurrences: 12, FirstSeenAt: "2026-09-01T09:00:00", LastSeenAt: "2026-09-19T06:00:00",
	}
}

func errored() faults.Incident {
	return faults.Incident{
		ID: 8, Code: "ERR-0002", Severity: faults.SeverityError,
		Project: "acme", Source: "job:exploding",
		Summary:     `job "exploding" panicked`,
		Occurrences: 1, FirstSeenAt: "2026-09-19T05:00:00", LastSeenAt: "2026-09-19T05:00:00",
	}
}

func cleared() faults.Incident {
	i := errored()
	i.ID, i.ClearedAt, i.ClearedBy = 9, "2026-09-19T05:30:00", "mike"
	return i
}

func wideRunes() faults.Incident {
	i := warned()
	// Double-width runes are where a rune-counting layout silently overflows —
	// tabwriter counted these as one column each and shifted every column after
	// them.
	//
	// The wide runes go in the PROJECT cell as well as the summary, deliberately:
	// a wide SUMMARY can only overrun the line, because it is last, while a wide
	// PROJECT throws every column after it out of alignment. Both are failures
	// this layout has to not have, and only the second needs a column to its
	// right to show up.
	i.ID, i.Project, i.Summary = 10, "日本語プロジェクト", strings.Repeat("日本語のテキスト", 10)
	return i
}

// widths is the sweep: absurdly narrow through wider than any real terminal.
func widths() (out []int) {
	for w := 1; w <= 240; w++ {
		out = append(out, w)
	}
	return out
}

// cases is every shape the listing takes: scoped or machine-wide, open-only or
// including history, ASCII or wide runes.
func cases() map[string]struct {
	incidents      []faults.Incident
	wide           bool
	includeCleared bool
} {
	return map[string]struct {
		incidents      []faults.Incident
		wide           bool
		includeCleared bool
	}{
		"scoped":      {[]faults.Incident{warned(), errored()}, false, false},
		"wide":        {[]faults.Incident{warned(), errored()}, true, false},
		"with status": {[]faults.Incident{warned(), cleared()}, false, true},
		"everything":  {[]faults.Incident{warned(), cleared()}, true, true},
		"wide runes":  {[]faults.Incident{wideRunes(), errored()}, true, true},
	}
}

func TestListingLines_NeverExceedTheTerminalWidth(t *testing.T) {
	for name, c := range cases() {
		for _, cols := range widths() {
			for _, line := range listingLines(c.incidents, c.wide, c.includeCleared, cols) {
				if strings.Contains(line, "\n") {
					t.Fatalf("%s/cols=%d: a row contains a newline:\n%q", name, cols, line)
				}
				// cols-1, not cols: a line ending exactly at the right margin
				// sits on the deferred-wrap boundary where some emulators emit
				// a phantom second row.
				if got := runewidth.StringWidth(line); got > cols-1 {
					t.Fatalf("%s/cols=%d: printed width %d exceeds cols-1:\n%q",
						name, cols, got, line)
				}
			}
		}
	}
}

func TestListingLines_AreColumnAlignedAtEveryWidth(t *testing.T) {
	// A table whose rows disagree about where a column starts is not a table.
	// This is what tabwriter got wrong on wide runes: it pads by rune count, so
	// a CJK cell shifted every column to its right out of line.
	//
	// Alignment is checked on SOURCE, which sits to the right of PROJECT — the
	// wide-rune cell — and whose values contain no spaces, so its offset can be
	// located unambiguously in a rendered row.
	for name, c := range cases() {
		for _, cols := range widths() {
			lines := listingLines(c.incidents, c.wide, c.includeCleared, cols)
			if len(lines) < 2 || !strings.Contains(lines[0], "SOURCE") {
				continue // the column shed at this width; nothing to align
			}
			want := offsetOf(lines[0], "SOURCE")
			for r, incident := range c.incidents {
				got := offsetOf(lines[r+1], incident.Source)
				if got != want {
					t.Fatalf("%s/cols=%d: SOURCE starts at column %d on the heading "+
						"and %d on a row:\n%q\n%q", name, cols, want, got, lines[0], lines[r+1])
				}
			}
		}
	}
}

// offsetOf is where needle begins in line, measured in DISPLAY columns — which
// is the measure a terminal aligns by, and the one a rune count silently
// diverges from. -1 when absent.
func offsetOf(line, needle string) (col int) {
	at := strings.Index(line, needle)
	if at < 0 {
		return -1
	}
	return runewidth.StringWidth(line[:at])
}

func TestListingLines_WideRuneFixtureIsActuallyWide(t *testing.T) {
	// Guard the guard. Every check above rests on a fixture whose display width
	// exceeds its rune count; if that ever stopped being true the wide-rune case
	// would pass by saying nothing.
	i := wideRunes()
	for _, cell := range []string{i.Project, i.Summary} {
		if runewidth.StringWidth(cell) <= len([]rune(cell)) {
			t.Errorf("fixture cell %q is not double-width; the layout tests are vacuous", cell)
		}
	}
}

func TestListingLines_SpendNoColumnOnSeverity(t *testing.T) {
	// The code says it: WARN-0004 is a warning because it is spelled WARN-0004.
	// A SEVERITY column would state that on every row at a cost of ten columns,
	// taken from the summary — the part that was being cut off.
	for name, c := range cases() {
		lines := listingLines(c.incidents, c.wide, c.includeCleared, 0)
		if strings.Contains(lines[0], "SEVERITY") {
			t.Errorf("%s: the listing still carries a SEVERITY column:\n%q", name, lines[0])
		}
		for _, line := range lines[1:] {
			for _, word := range []string{"warning", "error"} {
				if strings.Contains(line, word) {
					t.Errorf("%s: a row still spells out %q:\n%q", name, word, line)
				}
			}
		}
	}
}

func TestListingLines_SayWhetherARowIsClearedOnlyUnderAll(t *testing.T) {
	// "(cleared)" used to ride inside the severity cell. With severity gone it
	// has its own column — and only in the mode that can produce a cleared row.
	open := listingLines([]faults.Incident{warned(), cleared()}, false, false, 0)
	if strings.Contains(open[0], "STATUS") {
		t.Errorf("a default listing carries a STATUS column every row of which says open:\n%q", open[0])
	}

	all := listingLines([]faults.Incident{warned(), cleared()}, false, true, 0)
	if !strings.Contains(all[0], "STATUS") {
		t.Fatalf("--all does not say which rows are history:\n%q", all[0])
	}
	if !strings.Contains(all[1], "open") {
		t.Errorf("an open row is not marked open:\n%q", all[1])
	}
	if !strings.Contains(all[2], "cleared") {
		t.Errorf("a cleared row is not marked cleared:\n%q", all[2])
	}
}

func TestListingLines_KeepTheIdTheCodeAndASummaryWheneverATableFits(t *testing.T) {
	// Those three ARE the listing: the id you type into the next command, the
	// code that says what went wrong and how badly, and enough summary to tell
	// two incidents apart. Everything else may shed.
	//
	// The threshold is DERIVED, not chosen: the three headings, the two gaps
	// between them, a code's width, a minimum summary, and the one column held
	// back from the deferred-wrap margin. Below it no table exists to protect —
	// the row is clamped to whatever the terminal can hold.
	incidents := []faults.Incident{warned(), errored()}
	minTable := runewidth.StringWidth("ID") + runewidth.StringWidth("WARN-0004") +
		minSummary + 2*len(gap) + 1

	for _, cols := range widths() {
		lines := listingLines(incidents, true, true, cols)
		if len(lines) == 0 {
			t.Fatalf("cols=%d: the listing rendered nothing at all", cols)
		}
		if cols < minTable {
			continue
		}
		head := lines[0]
		for _, want := range []string{"ID", "CODE", "SUM"} {
			if !strings.Contains(head, want) {
				t.Fatalf("cols=%d: heading lost %q:\n%q", cols, want, head)
			}
		}
		for _, code := range []string{"WARN-0004", "ERR-0002"} {
			if !strings.Contains(strings.Join(lines[1:], "\n"), code) {
				t.Fatalf("cols=%d: a row lost its whole code %q:\n%q", cols, code, lines)
			}
		}
	}
}

func TestListingLines_ShedWholeColumnsInAFixedOrder(t *testing.T) {
	// Narrowing must remove columns, not smear them. SOURCE goes first and
	// COUNT last, so the same terminal always yields the same table.
	incidents := []faults.Incident{warned(), errored()}

	shedAt := map[string]int{}
	for _, cols := range widths() {
		head := listingLines(incidents, true, true, cols)[0]
		for _, name := range []string{"SOURCE", "LAST SEEN", "PROJECT", "STATUS", "COUNT"} {
			if !strings.Contains(head, name) {
				if _, seen := shedAt[name]; !seen {
					shedAt[name] = cols
				}
				continue
			}
			// Present again at a wider width is fine; present again at a
			// NARROWER one would mean the order is not fixed.
			if at, seen := shedAt[name]; seen && cols < at {
				t.Fatalf("%s came back at cols=%d after shedding at cols=%d", name, cols, at)
			}
		}
	}

	order := []string{"SOURCE", "LAST SEEN", "PROJECT", "STATUS", "COUNT"}
	for i := 1; i < len(order); i++ {
		if shedAt[order[i-1]] < shedAt[order[i]] {
			t.Errorf("%s sheds before %s (at cols %d and %d); the order is wrong",
				order[i], order[i-1], shedAt[order[i]], shedAt[order[i-1]])
		}
	}
}

func TestListingLines_GiveTheSummaryEveryColumnLeftOver(t *testing.T) {
	// The whole point of dropping SEVERITY: reclaim the width for the part that
	// was being cut off. A wider terminal must show MORE summary, never the same
	// amount with padding on the right.
	incidents := []faults.Incident{warned()}

	prev := 0
	for _, cols := range []int{60, 80, 100, 120, 160, 200} {
		row := listingLines(incidents, false, false, cols)[1]
		got := runewidth.StringWidth(strings.TrimRight(row, " "))
		if got <= prev {
			t.Errorf("cols=%d: the row is no longer than at the previous width (%d vs %d)",
				cols, got, prev)
		}
		prev = got
	}
}

func TestListingLines_AreEmptyWithNoIncidents(t *testing.T) {
	// No rows means no heading either: the header line above the table already
	// said "no errors in <project>", and a heading over nothing would read as a
	// table that failed to render.
	if got := listingLines(nil, false, false, 100); len(got) != 0 {
		t.Errorf("an empty listing rendered %d lines: %q", len(got), got)
	}
}
