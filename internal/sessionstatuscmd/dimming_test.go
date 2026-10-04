package sessionstatuscmd

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// E-2204: rows that are not actionable from this board dim, highlighted ids are
// never faded by that dim, and a task that has just become spawnable is
// highlighted green until someone claims it.

// spawnableRow is a ready task one of whose blockers has released it.
func spawnableRow(id int64) monitor.SessionStatusRow {
	return monitor.SessionStatusRow{ID: id, Status: "ready", Phase: "now", ReleasedByN: 1}
}

func TestColorize_DimsNonActionableRows(t *testing.T) {
	cases := []struct {
		name string
		row  monitor.SessionStatusRow
		want intensity
	}{
		{"in-flight row", monitor.SessionStatusRow{Status: "underway", Phase: "now", InFlight: true}, intensityDim},
		{"in-flight urgent row", monitor.SessionStatusRow{Status: "underway", Phase: "urgent", InFlight: true}, intensityDim},
		{"parent row", monitor.SessionStatusRow{Status: "underway", Phase: "now", IsParent: true}, intensityDim},
		{"urgent parent row", monitor.SessionStatusRow{Status: "underway", Phase: "urgent", IsParent: true}, intensityDim},
		{"unsettled in-flight row still dims", monitor.SessionStatusRow{Status: "underway", Phase: "now", InFlight: true, Unsettled: true}, intensityDim},
		{"unsettled parent row still dims", monitor.SessionStatusRow{Status: "underway", Phase: "now", IsParent: true, Unsettled: true}, intensityDim},
		{"unsettled later row keeps E-1707's veto", monitor.SessionStatusRow{Status: "ready", Phase: "later", Unsettled: true}, intensityNormal},
		{"verify row stays bright", monitor.SessionStatusRow{Status: "unverified", Phase: "now"}, intensityNormal},
		{"board's own task", monitor.SessionStatusRow{Status: "underway", Phase: "now", IsFocal: true}, intensityNormal},
		{"urgent board task stays bold", monitor.SessionStatusRow{Status: "underway", Phase: "urgent", IsFocal: true}, intensityBold},
		{"plain urgent row stays bold", monitor.SessionStatusRow{Status: "ready", Phase: "urgent"}, intensityBold},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rowIntensity(c.row); got != c.want {
				t.Errorf("rowIntensity = %d, want %d", got, c.want)
			}
		})
	}
	// colorize and rowIntensity agree, by construction; spot-check the escape.
	inFlight := monitor.SessionStatusRow{Status: "underway", Phase: "now", InFlight: true}
	if got := colorize("x", inFlight, true); got != "\x1b[2mx\x1b[0m" {
		t.Errorf("colorize(in-flight) = %q, want dim", got)
	}
	if got := colorize("x", inFlight, false); got != "x" {
		t.Errorf("colorize with colour off = %q, want no escapes", got)
	}
}

// TestIDField_HighlightLiftsOutOfDim: on a dimmed row the highlight cancels dim
// (22m) before it and restores it (2m) after, so the colours keep their
// contrast; a row that is not dimmed gets no such wrapping.
func TestIDField_HighlightLiftsOutOfDim(t *testing.T) {
	dupInFlight := monitor.SessionStatusRow{ID: 42, Status: "underway", Phase: "now", InFlight: true, DuplicateWork: true}
	want := "\x1b[22m\x1b[38;5;232;48;5;160mE-42\x1b[39;49m\x1b[2m  "
	if got := idField(dupInFlight, true); got != want {
		t.Errorf("duplicate on in-flight row: %q, want %q", got, want)
	}
	focusParent := monitor.SessionStatusRow{ID: 42, Status: "underway", Phase: "now", IsParent: true, Focused: true}
	want = "\x1b[22m\x1b[7mE-42\x1b[27m\x1b[2m  "
	if got := idField(focusParent, true); got != want {
		t.Errorf("focus on parent row: %q, want %q", got, want)
	}
	if got := idField(dupInFlight, false); got != "E-42  " {
		t.Errorf("colour off: %q, want no escapes", got)
	}
	plainDim := monitor.SessionStatusRow{ID: 42, Status: "underway", Phase: "now", InFlight: true}
	if got := idField(plainDim, true); got != "E-42  " {
		t.Errorf("unhighlighted dim row: %q, want no escapes", got)
	}
}

func TestIDField_Spawnable(t *testing.T) {
	if got, want := idField(spawnableRow(42), true), "\x1b[38;5;232;48;5;118mE-42\x1b[39;49m  "; got != want {
		t.Errorf("spawnable: %q, want %q", got, want)
	}
	// Spawnable in a dimmed phase is lifted out of the dim like the others.
	later := spawnableRow(42)
	later.Phase = "later"
	if got, want := idField(later, true), "\x1b[22m\x1b[38;5;232;48;5;118mE-42\x1b[39;49m\x1b[2m  "; got != want {
		t.Errorf("spawnable later: %q, want %q", got, want)
	}
}

// TestHighlightPrecedence: duplicate (red) outranks spawnable (green), which
// outranks focus (inverse), on the id and in column 4 alike.
func TestHighlightPrecedence(t *testing.T) {
	both := spawnableRow(42)
	both.DuplicateWork = true
	both.Focused = true
	if got := idField(both, true); !strings.HasPrefix(got, "\x1b[38;5;232;48;5;160m") {
		t.Errorf("duplicate+spawnable id: %q, want the duplicate colours", got)
	}
	if got := columnFourMark(both); got != duplicateGlyph {
		t.Errorf("duplicate+spawnable mark: %q, want %q", got, duplicateGlyph)
	}
	both.DuplicateWork = false
	if got := idField(both, true); !strings.HasPrefix(got, "\x1b[38;5;232;48;5;118m") {
		t.Errorf("spawnable+focus id: %q, want the spawnable colours", got)
	}
	if got := columnFourMark(both); got != spawnableGlyph {
		t.Errorf("spawnable+focus mark: %q, want %q", got, spawnableGlyph)
	}
}

// TestRender_SpawnableLegend: a plain frame shows ▷ in column 4 and documents it
// in the legend.
func TestRender_SpawnableLegend(t *testing.T) {
	r := spawnableRow(150)
	r.Title, r.TypeSlug, r.UnsettledKnown = "now unblocked", "todo", true
	rows := []monitor.SessionStatusRow{
		{ID: 100, Title: "claimed", Status: "underway", Phase: "now", TypeSlug: "todo", IsFocal: true, UnsettledKnown: true},
		r,
	}
	var b strings.Builder
	renderTo(&b, rows, 100, hintClaimBind, 200, false, hiddenOmit)
	out := b.String()
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], spawnableGlyph+" unblocked") {
		t.Errorf("legend %q must document %q", lines[0], spawnableGlyph+" unblocked")
	}
	if !strings.Contains(out, spawnableGlyph+"E-150") {
		t.Errorf("row must carry %q in column 4:\n%s", spawnableGlyph, out)
	}
}
