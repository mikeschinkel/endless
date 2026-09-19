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
// scarce, so the incident text and the command that explains it share it: text
// filling from the left, hint right-aligned. The text is what gives when the
// terminal is narrow — the hint is reserved out of the budget first, because a
// notification whose reader cannot act on it is just noise.
//
// # No severity word (E-2148)
//
// The row used to open with a nine-column chip reading " WARNING " or
// " ERROR   ". It does not any more: the code says it. WARN-0004 is a warning
// because it is spelled WARN-0004, checked by the catalog's own tests, so a word
// beside it would state the same fact twice — at a cost of nine columns on a
// line whose scarcest resource is columns, taken from the summary, which is the
// part that was being cut off.
//
// The colour stays. The whole row is still reversed in the max severity's pair,
// which is what makes it read at a glance from across a pane; what went is the
// redundant TEXT, not the distinction.
//
// # What the line says
//
// ONE open incident — its code, then its summary:
//
//	WARN-0004 job scheduling row could not be created          Run eeh
//
// MORE THAN ONE — a severity tally in glyphs, then the distinct codes, most
// severe first:
//
//	✕2 ⚠1  ERR-0002 ERR-0011 WARN-0004                         Run eeh
//
// and no summary. With several incidents open, naming one summary reads as the
// whole story when it is a fraction of it; the codes say what KINDS of thing are
// wrong, which is the question a one-line notification can actually answer.
//
// Duplicate codes collapse — the tally already carries the count.

// Severity styling.
//
// These are 256-color indices, NOT the 30-47 ANSI range, because 30-47 is
// remapped by the terminal's theme: "yellow" arrives as whatever the user's
// theme decided yellow is, and the previous black-on-`43` chip rendered as an
// unreadable dark-orange block on exactly that path (E-1950). 256-color indices
// are fixed points in a standard cube — index 220 is the same gold everywhere.
//
// The WHOLE row is reversed, so the line reads as a bar rather than as a colored
// word floating in ordinary text. There used to be a second, inverted pair for
// the severity chip; the chip is gone (E-2148) and so is the pair.
const (
	rowError   = "\033[48;5;160;38;5;231m" // white on red
	rowWarning = "\033[48;5;220;38;5;16m"  // black on gold
	rowReset   = "\033[0m"
)

// Severity glyphs for the multi-incident tally.
//
// Both are single-width under runewidth, which is load-bearing: this row is
// budgeted in terminal columns, and a double-width glyph counted as one would
// push the right-aligned hint past the margin and wrap the line — the one thing
// this row must never do. TestRowLine_TallyGlyphsAreSingleWidth pins it, so
// swapping a glyph for a prettier double-width one fails rather than ships.
//
// Deliberately NOT emoji. An emoji presentation selector makes width
// terminal-dependent, which is exactly the property a column budget cannot
// tolerate.
const (
	glyphError   = "✕"
	glyphWarning = "⚠"
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

// rowLine assembles the single notification row: text left, right-aligned hint,
// all on one reversed background painted across the line.
//
// It fills cols-1, not cols. A line that ends exactly at the right margin sits
// on the terminal's deferred-wrap boundary, where some emulators — tmux panes
// among them — emit a spurious second row. The monitor sizes its pane from
// frameLines, which counts newlines, so a visual wrap it cannot see would
// mis-fit the pane. One unpainted column at the right edge is invisible; a
// wrapped row is not.
//
// Terminal width is not a fixed property of anyone's setup — it changes with the
// monitor, the split, the font and the window — so every branch below is derived
// from `cols` rather than tuned against any particular one. The invariant the
// width-sweep tests hold it to: the printed width never exceeds cols-1, at any
// width, for any content, in color or out.
//
// Degradation order as the row narrows: the hint goes first (it is reserved
// before the text, so it survives every width where both fit), then the text
// sheds from the right — see rowText — and below that the row renders nothing
// at all. A blank reversed line would cost the same space and say nothing.
func rowLine(overview faults.Overview, cols int, color bool) (line string) {
	var text string
	var hint string
	var width int
	var budget int
	var pad int

	width = cols - 1
	if width < 1 {
		goto end
	}

	hint = Hint
	budget = textBudget(width, hint)
	if budget == width {
		// textBudget kept the whole span for the text, which is how it reports
		// that the hint does not fit.
		//
		// The decision is made on the BUDGET and not on what the text happened
		// to need, which is what keeps it monotonic: the hint appears at every
		// width above one threshold and at none below it. Deciding from the
		// fitted text instead made it blink back on as the terminal NARROWED —
		// a tally short enough to leave room where a code list had not — which
		// is the opposite of "the hint goes first".
		hint = ""
	}
	text = rowText(overview, budget)
	if text == "" {
		goto end
	}

	pad = width - runewidth.StringWidth(text) - runewidth.StringWidth(hint)
	if pad < 0 {
		pad = 0
	}

	if !color {
		line = strings.TrimRight(text+strings.Repeat(" ", pad)+hint, " ")
		goto end
	}

	line = rowStyle(overview.Max) + text + strings.Repeat(" ", pad) + hint + rowReset

end:
	return line
}

// rowText is the incident text, fitted into budget columns. It returns "" when
// nothing legible fits, which is rowLine's signal to render no row at all.
//
// One incident gets its code and summary; several get a glyph tally and the
// distinct codes. The split is not about space — it is about what a single line
// can honestly say. With one thing wrong, the summary IS the news. With four,
// naming one summary reads as the whole story when it is a quarter of it.
func rowText(overview faults.Overview, budget int) (text string) {
	if budget < 1 {
		goto end
	}
	if overview.Total == 1 && overview.Latest != nil {
		text = singleText(*overview.Latest, budget)
		goto end
	}
	text = multiText(overview, budget)

end:
	return text
}

// minSummaryFragment is the shortest summary tail worth printing beside a code.
//
// Below it the summary has stopped being a summary: "j…" tells a reader nothing
// the code did not, while costing the columns that would have gone to nothing
// else. The code alone is the better row.
const minSummaryFragment = 6

// singleText renders the one-incident row: code, then as much of the summary as
// fits.
//
// The code is never truncated. A code is an identifier — it is looked up, typed
// into `errors show`, and pasted into a bug report — and "WARN-00…" serves none
// of those, so a budget too narrow for the whole code renders no row rather than
// a code-shaped thing that is not one.
func singleText(incident faults.Incident, budget int) (text string) {
	var codeWidth int
	var left int

	codeWidth = runewidth.StringWidth(incident.Code)
	if codeWidth > budget {
		goto end
	}
	text = incident.Code

	left = budget - codeWidth - 1
	if left < minSummaryFragment {
		goto end
	}
	text += " " + runewidth.Truncate(liveview.Collapse(incident.Summary), left, "…")

end:
	return text
}

// multiText renders the several-incidents row: the severity tally, then the
// distinct codes.
//
// Degradation is by whole units, never by cutting one in half: the codes shed
// from the right into "+N more", and below that the tally stands alone. The
// tally is the last thing to go because it is the one part that is still true
// at any width — "two errors and a warning are open" needs no room to be
// understood.
func multiText(overview faults.Overview, budget int) (text string) {
	var tally string
	var codes string
	var left int

	tally = severityTally(overview)
	if tally == "" || runewidth.StringWidth(tally) > budget {
		goto end
	}
	text = tally

	left = budget - runewidth.StringWidth(tally) - 2
	if left < 1 {
		goto end
	}
	codes = fitCodes(overview.Codes, left)
	if codes == "" {
		goto end
	}
	text += "  " + codes

end:
	return text
}

// severityTally renders the per-severity counts as glyphs, most severe first:
// `✕2 ⚠1`. A severity with no open incidents is omitted rather than shown as
// zero — a zero is a fact nobody needs on a line this scarce.
//
// This is what the nine-column " WARNING " chip became. It says strictly more
// (how many, at each severity, not just the maximum) in a quarter of the space.
func severityTally(overview faults.Overview) (tally string) {
	errCount := overview.Counts[faults.SeverityError]
	warnCount := overview.Counts[faults.SeverityWarning]

	if errCount > 0 {
		tally = glyphError + strconv.Itoa(errCount)
	}
	if warnCount > 0 {
		if tally != "" {
			tally += " "
		}
		tally += glyphWarning + strconv.Itoa(warnCount)
	}

	return tally
}

// fitCodes renders as many codes as fit in budget, space-separated, replacing
// the remainder with `+N more`.
//
// It never emits a partial code — see singleText for why — and it never emits a
// bare `+N more` with no code beside it, which would be a longer way of saying
// what the tally already said. Either at least one whole code and an honest
// count of the rest fit, or nothing does and the caller keeps the tally alone.
func fitCodes(codes []string, budget int) (text string) {
	var joined string
	var candidate string
	var take int

	if len(codes) == 0 {
		goto end
	}

	joined = strings.Join(codes, " ")
	if runewidth.StringWidth(joined) <= budget {
		text = joined
		goto end
	}

	for take = len(codes) - 1; take >= 1; take-- {
		candidate = strings.Join(codes[:take], " ") +
			" +" + strconv.Itoa(len(codes)-take) + " more"
		if runewidth.StringWidth(candidate) <= budget {
			text = candidate
			goto end
		}
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

// minTextBudget is the narrowest incident text worth keeping the hint for.
//
// Below it the hint is costing more than it is worth: a row cut down to
// "WARN-0004 job sch…" has stopped telling the user what happened, and pointing
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
