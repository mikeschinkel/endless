// Package faultbadge renders the uncleared-fault badge (E-698) — the one-line
// annotation both live status views append below their rows.
//
// Extracted from internal/sessionstatuscmd by E-1976, which added a second
// caller: the project-scoped attention board. The badge annotates the MACHINE
// (open incidents in the faults store), not the view it hangs off, so neither
// view owns it. Moved wholesale, tests included — nothing about how it renders
// changed.
package faultbadge

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/liveview"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// The uncleared-fault badge (E-698).
//
// Rendered as a trailing line on every status view that hangs it: `session
// status` (one-shot), `session monitor` (looped), and since E-1976 the
// `project status` / `project monitor` pair. The one-shot surfaces matter as
// much as the looped ones — a fault raised while no monitor pane is open would
// otherwise be invisible.
//
// ONE line, always (E-1950). The badge sits in a status pane where every row is
// scarce, so the severity chip, the incident text, and the command that explains
// it share a row: chip left, text filling, hint right-aligned. The text is what
// gives when the terminal is narrow — the hint is reserved out of the budget
// first, because a badge whose reader cannot act on it is just noise.

// Severity styling.
//
// These are 256-color indices, NOT the 30-47 ANSI range, because 30-47 is
// remapped by the terminal's theme: "yellow" arrives as whatever the user's
// theme decided yellow is, and the previous black-on-`43` chip rendered as an
// unreadable dark-orange block on exactly that path (E-1950). 256-color indices
// are fixed points in a standard cube — index 220 is the same gold everywhere.
//
// The whole row is reversed, not just the chip, so the badge reads as a bar
// rather than as a colored word floating in ordinary text. The chip then
// inverts the row's own pair, which delineates it without introducing a third
// color that would have to be legible against both.
const (
	rowError    = "\033[48;5;160;38;5;231m" // white on red
	chipError   = "\033[48;5;231;38;5;160m" // red on white
	rowWarning  = "\033[48;5;220;38;5;16m"  // black on gold
	chipWarning = "\033[48;5;16;38;5;220m"  // gold on black
	badgeReset  = "\033[0m"
)

// Hint is the command the badge points at. It names a shell helper rather than
// the underlying `endless errors ...` invocation because the hint shares a row
// with the incident text it would otherwise crowd out.
//
// Exported (E-1976) because it is the badge's public contract: it is the one
// string by which a caller — or a caller's test — can identify the badge line
// inside a frame it did not render itself.
const Hint = "Run eeh"

// staleWarningAfter is how much ACTIVE time (see monitor.ActiveSecondsSince) may
// pass after a warning's last occurrence before it stops being badged.
//
// Warnings only. An error never ages off — it stays until someone clears it.
//
// This is the deliberate reversal of E-698's original "clearing is manual, so an
// intermittent fault cannot heal itself out of view" rule. That rule was written
// against a fault that is still happening; applied to one that happened once and
// self-healed, it pins a permanently unactionable warning to the pane. The
// incident is not deleted or cleared — `endless errors show` still lists it —
// it just stops occupying a row that has nothing left to say. Keying the hour to
// active rather than wall-clock time means the hour is one the user was actually
// present for.
const staleWarningAfter = time.Hour

// Render writes the badge line when incidents worth badging exist, and
// writes nothing at all otherwise.
//
// It NEVER fails the render. Any error reading the fault store — a missing table
// on a schema-passive connection, a locked DB — is swallowed and the badge is
// simply omitted. A diagnostics surface must not be able to take down the view
// it is annotating.
func Render(w io.Writer, cols int, color bool) {
	var incidents []faults.Incident
	var overview faults.Overview
	var line string
	var err error

	incidents, err = faults.List(false, 0)
	if err != nil {
		goto end
	}

	incidents = badgeworthy(incidents, monitor.ActiveSecondsSince)
	overview = faults.Summarize(incidents)
	if overview.Total == 0 {
		goto end
	}

	line = badgeLine(overview, cols, color)
	if line == "" {
		// Too narrow to render anything legible. A blank reversed row would be
		// worse than no row: it costs the same space and says nothing.
		goto end
	}

	fmt.Fprintln(w, line)

end:
	return
}

// badgeworthy drops the incidents that no longer earn a row: warnings whose last
// occurrence is more than staleWarningAfter of active time old.
//
// activeSince is taken as a parameter rather than called directly so the policy
// can be tested against a known clock. The production caller passes
// monitor.ActiveSecondsSince.
//
// On any failure to measure active time the incident is kept. The conservative
// direction is to keep showing a warning we cannot age out, never to hide one we
// cannot justify hiding.
func badgeworthy(
	incidents []faults.Incident,
	activeSince func(time.Time) (time.Duration, error),
) (kept []faults.Incident) {
	var active time.Duration
	var lastSeen time.Time
	var incident faults.Incident
	var err error

	kept = make([]faults.Incident, 0, len(incidents))
	for _, incident = range incidents {
		if incident.Severity != faults.SeverityWarning {
			kept = append(kept, incident)
			continue
		}
		lastSeen, err = time.Parse("2006-01-02T15:04:05", incident.LastSeenAt)
		if err != nil {
			// Unparseable timestamp: cannot age it out, so keep it.
			kept = append(kept, incident)
			continue
		}
		active, err = activeSince(lastSeen)
		if err != nil || active < staleWarningAfter {
			kept = append(kept, incident)
		}
	}

	return kept
}

// badgeLine assembles the single badge row: chip, text, right-aligned hint, all
// on one reversed background painted across the row.
//
// It fills cols-1, not cols. A line that ends exactly at the right margin sits
// on the terminal's deferred-wrap boundary, where some emulators — tmux panes
// among them — emit a spurious second row. The monitor sizes its pane from
// frameLines, which counts newlines, so a visual wrap it cannot see would
// mis-fit the pane. One unpainted column at the right edge is invisible; a
// wrapped badge is not.
// Terminal width is not a fixed property of anyone's setup — it changes with the
// monitor, the split, the font and the window — so every branch below is derived
// from `cols` rather than tuned against any particular one. The invariant the
// width-sweep tests hold it to: the printed width never exceeds cols-1, at any
// width, for any content, in color or out.
//
// Degradation order as the row narrows: the hint goes first (it is reserved
// before the text, so it survives every width where both fit), then the text
// truncates toward nothing, then the chip itself truncates. The severity is the
// last thing standing, because a badge that cannot say what happened is not
// worth the row it costs.
func badgeLine(overview faults.Overview, cols int, color bool) (line string) {
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
	// word, unpadded. It either fits whole or the badge renders nothing —
	// a sliver of a truncated word ("WARN", " ", "W") is not a badge, it is
	// debris occupying a row.
	if chipWidth+1 >= width {
		chip = strings.TrimSpace(chip)
		if runewidth.StringWidth(chip) > width {
			goto end
		}
		line = chip
		if color {
			line = chipStyle(overview.Max) + chip + badgeReset
		}
		goto end
	}

	text = badgeText(overview)
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
		chipStyle(overview.Max) + chip + badgeReset +
		rowStyle(overview.Max) + " " + text + strings.Repeat(" ", pad) + hint +
		badgeReset

end:
	return line
}

// badgeText is the incident text beside the chip: the latest incident's code and
// summary, prefixed by a count only when the count says something the chip does
// not.
//
// A lone warning behind a WARNING chip made the old badge read "WARNING 1
// warning — ..." (E-1950); the tally earns its space only when there is more
// than one incident, or when a lower severity is hiding behind a higher chip.
func badgeText(overview faults.Overview) (text string) {
	counts := badgeCounts(overview)

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

// badgeCounts renders the per-severity tallies, most severe first, and returns
// "" for the common single-incident case the chip already conveys.
func badgeCounts(overview faults.Overview) (text string) {
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

// rowStyle is the reversed background the whole badge row carries.
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

// severityLabel is the chip's text, padded to a common width so the badge lines
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
// Below it the hint is costing more than it is worth: a badge truncated to
// "ERR-0004 job sch…" has stopped telling the user what happened, and pointing
// them at a command to read more is no substitute for the text itself.
const minTextBudget = 20

// textBudget returns how many of `avail` columns the incident text may occupy
// beside the right-aligned hint. Returning `avail` unchanged means the hint does
// not fit and badgeLine should drop it.
//
// The hint is reserved BEFORE the text so it survives truncation at every width
// where both fit — a badge whose reader cannot act on it is just noise. The
// exception is a row too narrow for both, where the text wins.
//
// Never returns more than `avail`: overflowing here is what wraps the badge.
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
