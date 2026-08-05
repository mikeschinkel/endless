package sessionstatuscmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faults"
)

// The uncleared-fault badge (E-698).
//
// Rendered as a trailing line on BOTH `session status` (one-shot) and
// `session monitor` (looped), because they share this renderer and the one-shot
// is the more frequently seen surface — a fault raised while no monitor pane is
// open would otherwise be invisible.
//
// The badge shows the MAX severity present (error outranks warning), the count
// at each severity, the most recent incident's short text, and the command that
// explains it. An incident stays on the badge until `endless errors clear`:
// clearing is manual by design, so an intermittent fault cannot heal itself out
// of view before anyone has had a chance to investigate it.

// ANSI attributes for the severity chip. Black-on-yellow rather than the
// default foreground, because yellow backgrounds render white text illegibly on
// light terminal themes.
const (
	badgeError   = "\033[97;41m" // bright white on red
	badgeWarning = "\033[30;43m" // black on yellow
	badgeReset   = "\033[0m"
)

// renderFaultBadge writes the trailing badge line when uncleared incidents
// exist, and writes nothing at all otherwise.
//
// It NEVER fails the render. Any error reading the fault store — a missing table
// on a schema-passive connection, a locked DB — is swallowed and the badge is
// simply omitted. A diagnostics surface must not be able to take down the view
// it is annotating.
func renderFaultBadge(w io.Writer, cols int, color bool) {
	var overview faults.Overview
	var err error
	var chip string
	var text string

	overview, err = faults.Open()
	if err != nil || overview.Total == 0 {
		goto end
	}

	chip = severityChip(overview.Max, color)
	text = badgeCounts(overview)
	if overview.Latest != nil {
		text += " — " + overview.Latest.Code + " " + collapse(overview.Latest.Summary)
	}

	// Budget the text against the chip's PRINTED width, not its byte length:
	// the ANSI escapes occupy no columns.
	text = runewidth.Truncate(text, badgeTextBudget(cols, overview.Max), "…")

	fmt.Fprintln(w, chip+" "+text)
	fmt.Fprintln(w, dim("        endless errors show", color))

end:
	return
}

// severityChip renders the leading severity marker, colorized only when the
// caller established that the output is a color-capable terminal.
func severityChip(severity faults.Severity, color bool) (chip string) {
	label := severityLabel(severity)
	if !color {
		chip = label
		goto end
	}
	if severity == faults.SeverityError {
		chip = badgeError + label + badgeReset
		goto end
	}
	chip = badgeWarning + label + badgeReset

end:
	return chip
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

// badgeCounts renders the per-severity tallies, most severe first, omitting a
// severity with no open incidents.
func badgeCounts(overview faults.Overview) (text string) {
	errCount := overview.Counts[faults.SeverityError]
	warnCount := overview.Counts[faults.SeverityWarning]

	if errCount > 0 {
		text = strconv.Itoa(errCount) + " " + plural(errCount, "error", "errors")
	}
	if warnCount > 0 {
		if text != "" {
			text += ", "
		}
		text += strconv.Itoa(warnCount) + " " + plural(warnCount, "warning", "warnings")
	}
	return text
}

// badgeTextBudget returns how many columns the badge's text may occupy beside
// the chip, floored so a very narrow terminal still shows something.
func badgeTextBudget(cols int, severity faults.Severity) (budget int) {
	budget = cols - runewidth.StringWidth(severityLabel(severity)) - 1
	if budget < 20 {
		budget = 20
	}
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
