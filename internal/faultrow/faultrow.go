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
// # The chip holds the code, not the severity word (E-2148)
//
// The row opens with an inverted chip, as it always has. What CHANGED is what
// the chip holds: it read " WARNING " or " ERROR   ", and it now holds the
// code itself.
//
// The word was redundant the moment codes became severity-keyed. WARN-0004 is a
// warning because it is spelled WARN-0004, checked by the catalog's own tests —
// so a chip saying WARNING beside a code saying WARN stated one fact twice, and
// charged nine columns for the repetition on a line whose scarcest resource is
// columns. The chip's JOB, though, was never the word: it is to give the row a
// fixed left-hand anchor the eye lands on before it reads anything. Dropping
// the chip along with the word (a first pass at this did) threw that away to
// solve a problem the word alone had.
//
// So: chip kept, contents replaced. The reversed row still colours by max
// severity, and the chip still inverts that pair.
//
// # What the line says
//
// ONE open incident — the code in the chip, then its summary:
//
//	[ WARN-0004 ] job scheduling row could not be created       Run eeh
//
// MORE THAN ONE — a severity tally in glyphs in the chip, then the distinct
// codes, most severe first:
//
//	[ ✕2 ⚠1 ] ERR-0002 ERR-0011 WARN-0004                       Run eeh
//
// and no summary. With several incidents open, naming one summary reads as the
// whole story when it is a fraction of it; the codes say what KINDS of thing are
// wrong, which is the question a one-line notification can actually answer.
//
// The tally takes the chip in that case for the same reason the code takes it in
// the first: the chip holds whatever the row's leftmost, most-compressed
// statement of severity is, and with several open that is the tally.
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
// word floating in ordinary text. The chip then inverts the row's own pair,
// which delineates it without introducing a third color that would have to be
// legible against both.
const (
	rowError    = "\033[48;5;160;38;5;231m" // white on red
	chipError   = "\033[48;5;231;38;5;160m" // red on white
	rowWarning  = "\033[48;5;220;38;5;16m"  // black on gold
	chipWarning = "\033[48;5;16;38;5;220m"  // gold on black
	rowReset    = "\033[0m"
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
	var chip string
	var rest string
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

	chip = chipText(overview)
	if chip == "" {
		goto end
	}
	chipWidth = runewidth.StringWidth(chip)

	// No room for the padded chip plus anything beside it: fall back to the
	// chip unpadded. It either fits whole or the row renders nothing — a
	// truncated code ("WARN-00…") is not an identifier, and an identifier is
	// the whole of what this chip is for.
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

	// Columns left for the text and the hint, after the chip and the space
	// following it.
	avail = width - chipWidth - 1

	hint = Hint
	budget = textBudget(avail, hint)
	if budget == avail {
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
	rest = restText(overview, budget)

	pad = avail - runewidth.StringWidth(rest) - runewidth.StringWidth(hint)
	if pad < 0 {
		pad = 0
	}

	if !color {
		line = strings.TrimRight(chip+" "+rest+strings.Repeat(" ", pad)+hint, " ")
		goto end
	}

	line = rowStyle(overview.Max) +
		chipStyle(overview.Max) + chip + rowReset +
		rowStyle(overview.Max) + " " + rest + strings.Repeat(" ", pad) + hint +
		rowReset

end:
	return line
}

// chipText is the inverted block at the head of the row, padded by a space on
// each side so the reversed pair has breathing room around its contents.
//
// One incident: its CODE, which states both what happened and how severe it is.
// Several: the severity tally, which is the most compressed true statement
// available once no single code describes the situation.
//
// Never truncated, at any width — see rowLine's narrow branch. Returns "" only
// for an empty overview, which Render has already declined to draw.
func chipText(overview faults.Overview) (chip string) {
	if overview.Total == 1 && overview.Latest != nil {
		chip = overview.Latest.Code
		goto end
	}
	chip = severityTally(overview)

end:
	if chip == "" {
		goto done
	}
	chip = " " + chip + " "

done:
	return chip
}

// restText is what sits beside the chip, fitted into budget columns: the
// summary when one incident is open, the distinct codes when several are.
//
// The split is not about space — it is about what a single line can honestly
// say. With one thing wrong, the summary IS the news. With four, naming one
// summary reads as the whole story when it is a quarter of it.
//
// Returns "" when nothing fits, which is not a failure: the chip alone is a
// legitimate row, and at a narrow width it is the only honest one.
func restText(overview faults.Overview, budget int) (text string) {
	if budget < 1 {
		goto end
	}
	if overview.Total == 1 && overview.Latest != nil {
		text = runewidth.Truncate(liveview.Collapse(overview.Latest.Summary), budget, "…")
		if runewidth.StringWidth(text) < minSummaryFragment {
			// An ellipsis and a syllable is not a summary; it is debris in the
			// space the chip already used well.
			text = ""
		}
		goto end
	}
	text = fitCodes(overview.Codes, budget)

end:
	return text
}

// minSummaryFragment is the shortest summary tail worth printing beside the
// chip. Below it the summary has stopped being a summary: "j…" tells a reader
// nothing the code did not, while costing the columns that would have gone to
// nothing else.
const minSummaryFragment = 6

// chipStyle inverts rowStyle, so the chip reads as a distinct block within the
// bar.
func chipStyle(severity faults.Severity) (style string) {
	style = chipWarning
	if severity == faults.SeverityError {
		style = chipError
	}
	return style
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
