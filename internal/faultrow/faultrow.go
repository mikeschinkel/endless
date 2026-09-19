// Package faultrow renders the uncleared-fault notification row (E-698) — the
// one-line annotation both live status views append below their rows.
//
// Extracted from internal/sessionstatuscmd by E-1976, which added a second
// caller: `endless project status` and its live twin `project monitor`. Neither
// view owns the row, so it lives here; what it COUNTS is each view's own
// decision, passed in as a faults.ProjectScope (E-1960).
//
// The two callers answer that differently, and both are right. `session status`
// passes faults.AllProjects: it is a machine-wide view of every session on the
// box, so a fault in a project other than the one you are standing in is still
// news. `project status` passes its own project, because that view is already
// scoped to one project and a fault row counting other projects' incidents would
// be the one line on it that isn't. Unattributed faults ride along with both —
// see faults.ProjectScope for why they must.
//
// # The name
//
// Deliberately literal: it is a row, it notifies, and it is about faults. This
// surface spent a year under a coined label instead — one session invented it,
// later sessions cited the coinage as though it were settled product vocabulary,
// and E-2148 found the project's owner had never written the word anywhere and
// found it unintuitive. A name that exists only in text Claude produced is
// unratified, however many files carry it.
package faultrow

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/liveview"
)

// The uncleared-fault row (E-698).
//
// Rendered as a trailing line on every status view that hangs it: `session
// status` (one-shot), `session monitor` (looped), and since E-1976 the
// `project status` / `project monitor` pair. The one-shot surfaces matter as
// much as the looped ones — a fault raised while no monitor pane is open would
// otherwise be invisible.
//
// ONE line, always (E-1950). The row sits in a status pane where every line is
// scarce, so the severity chip, the incident text, and the command that explains
// it share it: chip left, text filling, hint right-aligned. The text is what
// gives when the terminal is narrow — the hint is reserved out of the budget
// first, because a notification whose reader cannot act on it is just noise.

// Severity styling.
//
// These are 256-color indices, NOT the 30-47 ANSI range, because 30-47 is
// remapped by the terminal's theme: "yellow" arrives as whatever the user's
// theme decided yellow is, and the previous black-on-`43` chip rendered as an
// unreadable dark-orange block on exactly that path (E-1950). 256-color indices
// are fixed points in a standard cube — index 220 is the same gold everywhere.
//
// The whole row is reversed, not just the chip, so the line reads as a bar
// rather than as a colored word floating in ordinary text. The chip then
// inverts the row's own pair, which delineates it without introducing a third
// color that would have to be legible against both.
const (
	rowError    = "\033[48;5;160;38;5;231m" // white on red
	chipError   = "\033[48;5;231;38;5;160m" // red on white
	rowWarning  = "\033[48;5;220;38;5;16m"  // black on gold
	chipWarning = "\033[48;5;16;38;5;220m"  // gold on black
	rowReset    = "\033[0m"
)

// Hint is the command the row points at. It names a shell helper rather than
// the underlying `endless errors ...` invocation because the hint shares a line
// with the incident text it would otherwise crowd out.
//
// Exported (E-1976) because it is the row's public contract: it is the one
// string by which a caller — or a caller's test — can identify the fault row
// inside a frame it did not render itself.
const Hint = "Run eeh"

// Nothing ages off the row. Neither severity expires, and every uncleared
// incident stays listed until someone clears it (E-698's original rule:
// clearing is manual, so an intermittent fault cannot heal itself out of view).
//
// A warning used to stop being shown an hour of active time after its last
// occurrence (E-1950), on the reasoning that a fault which happened once and
// self-healed had nothing left to say. It has one thing left to say: that it
// happened. Whether that matters is the user's call, and making it is one
// command — `eeh`, then `endless errors clear`.
//
// The age-off never deleted the incident — `endless errors list` still listed
// it — but the fault row is what makes a person think to run that command, so a
// warning that left the row had in practice left. That trades a line on a pane
// against a failure nobody ever learns about, and those costs are not
// comparable. Row noise is the user's to manage; the answer to too much of it
// is a better clearing affordance, not a timer deciding on their behalf which
// failures were unimportant.

// Render writes the fault row when uncleared incidents exist within scope, and
// writes nothing at all otherwise.
//
// scope is the caller's answer to "whose faults is this view responsible for":
// faults.AllProjects for a machine-wide view, a project's id for a view already
// scoped to it. Either way the unattributed incidents are included, because a
// machine-level failure has no other view to be reported on.
//
// It NEVER fails the render. Any error reading the fault store — a missing table
// on a schema-passive connection, a locked DB — is swallowed and the row is
// simply omitted. A diagnostics surface must not be able to take down the view
// it is annotating.
func Render(w io.Writer, cols int, color bool, scope faults.ProjectScope) {
	var overview faults.Overview
	var line string
	var err error

	overview, err = faults.Open(scope)
	if err != nil {
		goto end
	}
	if overview.Total == 0 {
		goto end
	}

	line = rowLine(overview, cols, color)
	if line == "" {
		// Too narrow to render anything legible. A blank reversed line would be
		// worse than no line: it costs the same space and says nothing.
		goto end
	}

	fmt.Fprintln(w, line)

end:
	return
}

// rowLine assembles the single notification row: chip, text, right-aligned hint,
// all on one reversed background painted across the line.
//
// It fills cols-1, not cols. A line that ends exactly at the right margin sits
// on the terminal's deferred-wrap boundary, where some emulators — tmux panes
// among them — emit a spurious second row. The monitor sizes its pane from
// frameLines, which counts newlines, so a visual wrap it cannot see would
// mis-fit the pane. One unpainted column at the right edge is invisible; a
// wrapped row is not.
// Terminal width is not a fixed property of anyone's setup — it changes with the
// monitor, the split, the font and the window — so every branch below is derived
// from `cols` rather than tuned against any particular one. The invariant the
// width-sweep tests hold it to: the printed width never exceeds cols-1, at any
// width, for any content, in color or out.
//
// Degradation order as the row narrows: the hint goes first (it is reserved
// before the text, so it survives every width where both fit), then the text
// truncates toward nothing, then the chip itself truncates. The severity is the
// last thing standing, because a row that cannot say what happened is not
// worth the line it costs.
func rowLine(overview faults.Overview, cols int, color bool) (line string) {
	var chip string
	var text string
	var hint string
	var width int
	var chipWidth int
	var avail int
	var budget int
	var pad int

	width = cols - 1
	if width < 1 {
		goto end
	}

	chip = severityLabel(overview.Max)
	chipWidth = runewidth.StringWidth(chip)

	// No room for the padded chip plus any text: fall back to the bare severity
	// word, unpadded. It either fits whole or the row renders nothing —
	// a sliver of a truncated word ("WARN", " ", "W") is not a notification, it
	// is debris occupying a line.
	if chipWidth+1 >= width {
		chip = strings.TrimSpace(chip)
		if runewidth.StringWidth(chip) > width {
			goto end
		}
		line = chip
		if color {
			line = chipStyle(overview.Max) + chip + rowReset
		}
		goto end
	}

	text = rowText(overview)
	hint = Hint

	// Columns left for text + hint, after the chip and the space following it.
	avail = width - chipWidth - 1

	budget = textBudget(avail, hint)
	if budget == avail {
		// textBudget kept the whole span for the text, which is how it reports
		// that the hint does not fit.
		hint = ""
	}
	text = runewidth.Truncate(text, budget, "…")

	pad = avail - runewidth.StringWidth(text) - runewidth.StringWidth(hint)
	if pad < 0 {
		pad = 0
	}

	if !color {
		line = strings.TrimRight(chip+" "+text+strings.Repeat(" ", pad)+hint, " ")
		goto end
	}

	line = rowStyle(overview.Max) +
		chipStyle(overview.Max) + chip + rowReset +
		rowStyle(overview.Max) + " " + text + strings.Repeat(" ", pad) + hint +
		rowReset

end:
	return line
}

// rowText is the incident text beside the chip: the latest incident's code and
// summary, prefixed by a count only when the count says something the chip does
// not.
//
// A lone warning behind a WARNING chip made the old row read "WARNING 1
// warning — ..." (E-1950); the tally earns its space only when there is more
// than one incident, or when a lower severity is hiding behind a higher chip.
func rowText(overview faults.Overview) (text string) {
	counts := rowCounts(overview)

	if counts != "" {
		text = counts
	}
	if overview.Latest != nil {
		if text != "" {
			text += " — "
		}
		text += overview.Latest.Code + " " + liveview.Collapse(overview.Latest.Summary)
	}

	return text
}

// rowCounts renders the per-severity tallies, most severe first, and returns
// "" for the common single-incident case the chip already conveys.
func rowCounts(overview faults.Overview) (text string) {
	errCount := overview.Counts[faults.SeverityError]
	warnCount := overview.Counts[faults.SeverityWarning]

	if overview.Total < 2 {
		goto end
	}

	if errCount > 0 {
		text = strconv.Itoa(errCount) + " " + plural(errCount, "error", "errors")
	}
	if warnCount > 0 {
		if text != "" {
			text += ", "
		}
		text += strconv.Itoa(warnCount) + " " + plural(warnCount, "warning", "warnings")
	}

end:
	return text
}

// rowStyle is the reversed background the whole notification row carries.
func rowStyle(severity faults.Severity) (style string) {
	style = rowWarning
	if severity == faults.SeverityError {
		style = rowError
	}
	return style
}

// chipStyle inverts rowStyle, so the severity label reads as a distinct block
// within the bar.
func chipStyle(severity faults.Severity) (style string) {
	style = chipWarning
	if severity == faults.SeverityError {
		style = chipError
	}
	return style
}

// severityLabel is the chip's text, padded to a common width so the row lines
// up whichever severity is showing.
func severityLabel(severity faults.Severity) (label string) {
	label = " WARNING "
	if severity == faults.SeverityError {
		label = " ERROR   "
	}
	return label
}

// minTextBudget is the narrowest incident text worth keeping the hint for.
//
// Below it the hint is costing more than it is worth: a row truncated to
// "ERR-0004 job sch…" has stopped telling the user what happened, and pointing
// them at a command to read more is no substitute for the text itself.
const minTextBudget = 20

// textBudget returns how many of `avail` columns the incident text may occupy
// beside the right-aligned hint. Returning `avail` unchanged means the hint does
// not fit and rowLine should drop it.
//
// The hint is reserved BEFORE the text so it survives truncation at every width
// where both fit — a notification whose reader cannot act on it is just noise.
// The exception is a row too narrow for both, where the text wins.
//
// Never returns more than `avail`: overflowing here is what wraps the row.
func textBudget(avail int, hint string) (budget int) {
	reserved := runewidth.StringWidth(hint) + 2

	budget = avail - reserved
	if budget >= minTextBudget {
		goto end
	}

	// Too tight for both — the text takes the whole span.
	budget = avail

end:
	return budget
}

// plural picks the singular or plural form for n.
func plural(n int, singular, many string) (word string) {
	word = many
	if n == 1 {
		word = singular
	}
	return word
}
